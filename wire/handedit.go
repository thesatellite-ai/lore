package wire

// handedit.go — the pick-up of hand edits (plain files round, brief 2.1–2.3,
// D-h): a store is a plain git clone a person may open and edit with any
// text editor, so every lore command that opens a store first looks at
// what changed in its files since lore's last commit and takes each row
// file through the one write path.
//
//	open ─▶ engine built, caches read from the last commits; no fetch yet
//	   (withDeferredFetch: a merge must never run over files edited by hand)
//	   ─▶ pickUpHandEdits: per store clone, `git status` of row paths
//	        edited file  ─▶ engine.PickUp: changed fields through Update's
//	                        checks (schema, references, secret scan,
//	                        policy), re-stamped (new HLC, updated_by = the
//	                        person, updated_at)
//	        new file     ─▶ engine.CreateWithID (the id the file names)
//	        deleted file ─▶ engine.Delete: lore's delete, into the trash
//	                        (`lore trash restore <id>` brings it back)
//	        file moved to another folder (same id) ─▶ engine.MoveWith
//	   ─▶ one commit per store folder, "hand edit: <table> <id>" (several:
//	      "hand edit: N rows", each listed in the body), authored by the
//	      person's git identity for the clone (D-h); a move is its own
//	      commit, "hand move: …"
//	   ─▶ each one printed: what, which file, which store, the commit
//	   ─▶ the caches read again with fetching on: the store's push mode
//	      pushes the new commits (and prints its push line) like any write
//	refused file ─▶ "file:line: reason" and the next step; the file stays as
//	   the person left it, never committed or pushed until fixed (a sync
//	   sets it aside for its merge and puts it back); the other edits go
//
// It runs for reads too ("any lore command touching a store"): a hand
// edit is the person's own write, and the pick-up says what it committed
// and where, so nothing is written silently. Inside a git hook (a hand
// `git commit` running lore's check) it never runs: the person's commit
// holds the index.
//
// Code repos (2.7): a hand edit of a row file lore knows in .lore/data is
// re-stamped the same way through the same path; lore never commits
// there (the person commits rows with the code), so the file is written
// and left for that commit.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/reposync"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// EnvGitHook marks a lore run started by lore's own git hook in a store
// clone (the hook block exports it): the pick-up never runs there.
const EnvGitHook = "LORE_GIT_HOOK"

// envGitIndexFile is set by git for every hook of a commit (pre-commit,
// prepare-commit-msg, commit-msg, post-commit), naming the index being
// committed.
const envGitIndexFile = "GIT_INDEX_FILE"

// inCommitHook reports whether this run is a hook of a `git commit` in
// progress: a store clone's hook block exports EnvGitHook, and git sets
// GIT_INDEX_FILE for a code repo's commit hooks (whose block, installed by
// older lore releases too, exports nothing). Under a commit the pick-up of
// hand edits must not run (the store sync after it still does, so the
// post-commit landing sees fresh stores): the commit holds the checkout, the person's staged files
// are what is being checked, and re-reading every changed row file there
// (a `lore fix` migration commit changes thousands) only slows each hook.
func inCommitHook() bool {
	return os.Getenv(EnvGitHook) != "" || os.Getenv(envGitIndexFile) != ""
}

// Commit subjects of the pick-up.
const (
	handEditSubject = "hand edit: "
	handMoveSubject = "hand move: "
	handLinePrefix  = "lore: "
)

// deferFetchKey marks a context whose store reads and writes must not
// fetch, merge or push yet (open, before the pick-up).
type deferFetchKey struct{}

// withDeferredFetch returns ctx under which storePush neither fetches nor
// pushes: open reads the caches from the last commits, the pick-up
// commits the hand edits, and only then the stores sync (2.2: valid hand
// edits are committed before any pull).
func withDeferredFetch(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferFetchKey{}, true)
}

// handEditContext is the context the pick-up runs under: store syncs
// deferred (I-1). App.state runs the pick-up with the command's own
// context, whose reads fetched and merged the remote (the secret scan's
// and the reference checks' reads of a store) before the hand edit was
// committed, against "valid hand edits are committed before any pull"; the
// sync after the pick-up (pickUpThenSync) then fetches, merges and pushes.
func handEditContext(ctx context.Context) context.Context { return withDeferredFetch(ctx) }

// fetchDeferred reports whether ctx defers store syncs (withDeferredFetch).
func fetchDeferred(ctx context.Context) bool {
	v, _ := ctx.Value(deferFetchKey{}).(bool)
	return v
}

// handGitOption is what every store clone gets so plain git behaves (brief
// 2.4, D-g, adapters/backend/git/handgit.go): lore's merge driver and the
// pre-commit block. The block runs this binary (the bare name when `lore`
// on PATH is this file) and fails the commit when no lore can check it.
//
// Only a lore binary sets it up (lore, lore.exe, lore-<suffix>): a Go
// test binary (wire.test) running the wiring would write itself in as the
// hook and the driver, and git would then start a test run on every hand
// commit.
//
// say gets the line a write prints when it sets an unwired clone up
// (owner bug 6: a read never does), prefixed with the running command.
func handGitOption(say func(string)) gitbackend.Option {
	exe, err := os.Executable()
	name := strings.TrimSuffix(filepath.Base(exe), ".exe")
	if err != nil || strings.HasSuffix(name, ".test") || (name != reposync.BinaryName && !strings.HasPrefix(name, reposync.BinaryName+"-")) {
		return gitbackend.Join()
	}
	bin := reposync.RunningDriverBinary()
	return gitbackend.WithHandGit(gitbackend.HandGit{
		Driver: bin + " merge-driver %O %A %B %P",
		Hook:   storeHookLines(bin),
		Say: func(ctx context.Context, dir string, set []string) {
			if say != nil {
				say(handGitSetUpLine(ctx, dir, set))
			}
		},
	})
}

// handGitSetUpLine is the one short line said when lore wrote a store
// clone's git set-up, prefixed with the running command (wire/cmdname.go).
// It names the clone (under ~) and what kind of thing was set; `lore
// where` lists every file and key (W-4: the full list made it ~300 chars).
func handGitSetUpLine(ctx context.Context, dir string, set []string) string {
	return CommandPrefix(ctx) + "store clone " + underHome(dir) + ": set up for plain git (" + handGitParts(set) + "; `lore where` lists them)"
}

// handGitParts sums up what set (".git/config: merge.lore.driver",
// ".git/hooks/pre-commit", …) holds: the merge driver and/or the hooks.
func handGitParts(set []string) string {
	merge, hooks := false, false
	for _, s := range set {
		switch {
		case strings.Contains(s, "merge"):
			merge = true
		case strings.Contains(s, "hooks"):
			hooks = true
		}
	}
	switch {
	case merge && hooks:
		return "merge driver, pre-commit hook"
	case hooks:
		return "pre-commit hook"
	case merge:
		return "merge driver"
	}
	return strconv.Itoa(len(set)) + " setting(s)"
}

// storeHookLines is the pre-commit block of a store clone: lore's check of
// the staged row files (valid JSON and rows, no conflict markers), then
// its secret scan; no lore that can run refuses the commit.
func storeHookLines(bin string) []string {
	return []string{
		EnvGitHook + "=pre-commit; export " + EnvGitHook,
		"if command -v " + bin + " >/dev/null 2>&1; then",
		"  " + bin + " sync hook pre-commit || exit $?",
		"  " + bin + " check leaks --staged || exit $?",
		"else",
		`  echo "lore: this is a lore store clone and no lore on PATH can check this commit (rows and secrets); install lore, then commit again" >&2`,
		"  exit 1",
		"fi",
	}
}

// pickUpThenSync ends an open: the pick-up of hand edits, then the store
// caches read again with fetching on, so each store clone with a remote
// syncs per its push mode (the pick-up's commits included) as the first
// read of a command always did.
func (st *state) pickUpThenSync(ctx context.Context, opts Options) error {
	if os.Getenv(EnvGitHook) != "" {
		// lore's hook in a person's `git commit`: their commit holds the
		// clone, so no pick-up and no fetch or merge under it
		return nil
	}
	st.pickUpHandEdits(ctx, opts.Notice)
	for _, name := range st.eng.Stores() {
		if b := st.byName[name]; b == nil || !b.Capabilities().Remote {
			continue
		}
		if _, err := st.eng.Refresh(ctx, name); err != nil {
			return errs.Prefix("wire: refresh the cache of store "+string(name), err)
		}
	}
	return nil
}

// handCheckout is one checkout the pick-up looks at, with the attached
// store folders in it.
type handCheckout struct {
	dir    string
	gb     *gitbackend.Backend
	places map[string]store.Source // clean folder → attached store
}

// pickUpHandEdits is the pick-up (see the file comment). say gets every
// line; a failure is a line, never a refused command: the person's own
// command still runs on what is committed.
func (st *state) pickUpHandEdits(ctx context.Context, say func(string)) {
	if inCommitHook() || st.eng == nil {
		return
	}
	ctx = handEditContext(ctx)
	if say == nil {
		say = func(string) {}
	}
	byDir := map[string]*handCheckout{}
	var order []string
	for _, name := range st.eng.Stores() {
		gb := gitOf(st.byName[name])
		if gb == nil {
			continue
		}
		if !gb.CloneMode() {
			if name == store.SourceRepo && !st.eng.ReadOnly(name) {
				st.pickUpRepoEdits(ctx, gb, say)
			}
			continue
		}
		co, ok := byDir[gb.Dir()]
		if !ok {
			co = &handCheckout{dir: gb.Dir(), gb: gb, places: map[string]store.Source{}}
			byDir[gb.Dir()] = co
			order = append(order, gb.Dir())
			if set := gb.HandGitSetUp(); len(set) > 0 {
				// brief 3.2: what an explicit setup or sync wrote into the
				// clone's git set-up is said the time it is written (a
				// read writes none, owner bug 6)
				say(handGitSetUpLine(ctx, gb.Dir(), set))
			}
		}
		if st.eng.ReadOnly(name) {
			continue
		}
		if _, taken := co.places[gb.Folder()]; !taken {
			co.places[gb.Folder()] = name
		}
	}
	for _, dir := range order {
		st.pickUpCheckout(ctx, byDir[dir], say)
	}
}

// handMove is a row file moved by hand from one folder to another.
type handMove struct {
	from, to gitbackend.HandChange
}

// pickUpCheckout picks up the hand edits of one store clone.
func (st *state) pickUpCheckout(ctx context.Context, co *handCheckout, say func(string)) {
	if co.gb.GitBusy(ctx) {
		return // a git command holds the index (a person's commit running lore's hook)
	}
	// B2-9: a person's merge or rebase in the clone: the pick-up would
	// unstage (git reset), which ends it; leave the clone alone and say so.
	if op, way := co.gb.PersonGitOp(ctx); op != "" {
		say(handLinePrefix + gitbackend.GitOpInProgressError(co.dir, op, way).Message + " (" + way + ")")
		return
	}
	// A merge a killed lore run left is lore's debris, not a hand edit: it
	// is aborted before the files are compared (the sync that used to
	// abort it first now runs after the pick-up).
	if err := co.gb.RecoverOwnMerge(ctx); err != nil {
		say(handLinePrefix + "could not look for hand edits in " + co.dir + ": " + err.Error())
		return
	}
	rep, err := co.gb.HandChanges(ctx)
	if err != nil {
		say(handLinePrefix + "could not look for hand edits in " + co.dir + ": " + err.Error())
		return
	}
	if len(rep.Rows) == 0 {
		return
	}
	if err := co.gb.Unstage(ctx); err != nil {
		say(handLinePrefix + "could not pick up the hand edits in " + co.dir + ": " + err.Error())
		return
	}
	rows, moves := pairMoves(rep.Rows)
	groups := map[store.Source][]gitbackend.HandChange{}
	var srcs []store.Source
	for _, c := range rows {
		src, ok := co.places[c.Folder]
		if !ok {
			say(handLinePrefix + "hand edit of " + c.Abs + " waits: folder " + c.Folder + " of this store is not open in this command (or is read-only); run any lore command inside it to pick it up (cd " + folderDir(co.dir, c.Folder) + " && lore task list)")
			continue
		}
		if _, seen := groups[src]; !seen {
			srcs = append(srcs, src)
		}
		groups[src] = append(groups[src], c)
	}
	for _, src := range srcs {
		st.pickUpFolder(ctx, co, src, groups[src], say)
	}
	for _, m := range moves {
		st.pickUpMove(ctx, co, m, say)
	}
}

// folderDir is the absolute folder of a store folder in the clone at dir.
func folderDir(dir, folder string) string {
	if folder == rowfs.FolderRoot || folder == "" {
		return dir
	}
	return dir + string(os.PathSeparator) + strings.ReplaceAll(folder, "/", string(os.PathSeparator))
}

// pairMoves takes out of rows each deleted file whose id shows up as a new
// file in another folder: a move made by hand (same id, other folder).
func pairMoves(rows []gitbackend.HandChange) (rest []gitbackend.HandChange, moves []handMove) {
	added := map[ids.AnyID]int{}
	for i, c := range rows {
		if c.Kind == gitbackend.HandAdded {
			added[c.ID] = i
		}
	}
	paired := map[int]bool{}
	for i, c := range rows {
		if c.Kind != gitbackend.HandDeleted {
			continue
		}
		if j, ok := added[c.ID]; ok && !paired[j] && rows[j].Folder != c.Folder && rows[j].Table == c.Table {
			moves = append(moves, handMove{from: c, to: rows[j]})
			paired[i], paired[j] = true, true
		}
	}
	for i, c := range rows {
		if !paired[i] {
			rest = append(rest, c)
		}
	}
	return rest, moves
}

// picked is one hand edit staged for the folder's commit.
type picked struct {
	c    gitbackend.HandChange
	what string
	undo func() error
}

// pickUpFolder picks up the hand edits of one store folder: every valid
// one staged, then ONE commit.
func (st *state) pickUpFolder(ctx context.Context, co *handCheckout, src store.Source, changes []gitbackend.HandChange, say func(string)) {
	sctx := engine.Staging(ctx)
	var done []picked
	for _, c := range changes {
		p, ok := st.pickUpOne(sctx, co, src, c, say)
		if ok && p.what != "" {
			done = append(done, p)
		}
	}
	if len(done) == 0 {
		engine.Discard(sctx)
		return
	}
	subject := handEditSubject + string(done[0].c.Table) + " " + done[0].c.ID.String()
	if len(done) > 1 {
		subject = handEditSubject + strconv.Itoa(len(done)) + " rows"
	}
	body := make([]string, 0, len(done))
	for _, p := range done {
		body = append(body, p.what+": "+p.c.Path)
	}
	if _, err := engine.Commit(store.WithPersonAuthor(store.WithCommitMessage(sctx, subject, body...))); err != nil {
		var undo []error
		for _, p := range done {
			undo = append(undo, p.undo())
		}
		say(handLinePrefix + "could not commit the hand edits in store " + string(src) + " (" + co.dir + "): " + errors.Join(append([]error{err}, undo...)...).Error() + "; every file is as you left it; fix the cause and run the command again")
		return
	}
	say(handLinePrefix + "picked up " + handRowCount(len(done)) + " edited by hand in store " + string(src) + ": committed " + shortCommit(co.gb.HeadCommit(ctx)) + " \"" + subject + "\" in " + co.dir)
	for _, p := range done {
		say("  " + p.what + ": " + p.c.Abs)
	}
}

// handRowCount is "1 row" / "N rows".
func handRowCount(n int) string {
	if n == 1 {
		return "1 row"
	}
	return strconv.Itoa(n) + " rows"
}

// shortCommit is the 7-character form of a commit id ("" stays "").
func shortCommit(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// pickUpOne stages the write of one changed row file. ok false: refused
// (said, the file left as it was). A file whose fields did not change is
// put back as lore wrote it (what "").
func (st *state) pickUpOne(ctx context.Context, co *handCheckout, src store.Source, c gitbackend.HandChange, say func(string)) (picked, bool) {
	refuse := func(hand []byte, err error) (picked, bool) {
		say(handRefusal(co.dir, c, hand, err))
		return picked{}, false
	}
	var hand []byte
	if c.Kind != gitbackend.HandDeleted {
		b, err := os.ReadFile(c.Abs)
		if err != nil {
			return refuse(nil, errs.FileIO("read", c.Abs, err))
		}
		hand = b
	}
	ref := engine.Ref{Store: src, Table: c.Table, ID: c.ID}
	switch c.Kind {
	case gitbackend.HandDeleted:
		if err := co.gb.RestoreHead(ctx, c); err != nil {
			return refuse(nil, err)
		}
		if _, err := st.eng.Delete(ctx, ref); err != nil {
			return refuse(nil, errors.Join(err, gitbackend.PutBack(c, nil)))
		}
		return picked{c: c, what: "deleted " + rowName(c) + " (moved to the trash; lore trash restore --target " + c.ID.String() + " brings it back)", undo: func() error { return gitbackend.PutBack(c, nil) }}, true
	case gitbackend.HandAdded:
		h, err := decodeHand(c, hand)
		if err != nil {
			return refuse(hand, err)
		}
		if err := gitbackend.PutBack(c, nil); err != nil {
			return refuse(hand, err)
		}
		if _, err := st.eng.CreateWithID(ctx, src, c.Table, c.ID, h.Fields); err != nil {
			return refuse(hand, errors.Join(err, gitbackend.PutBack(c, hand)))
		}
		return picked{c: c, what: "added " + rowName(c), undo: func() error { return gitbackend.PutBack(c, hand) }}, true
	default:
		h, err := decodeHand(c, hand)
		if err != nil {
			return refuse(hand, err)
		}
		base, err := row.Decode(c.Base)
		if err != nil {
			return refuse(hand, errs.Wrap(errs.InvalidInput, "the committed version of this file is not a row lore can read", err).
				WithHint("restore it from the store's history (git -C "+co.dir+" log -- "+c.Path+")"))
		}
		fields := engine.PickUpChanged(base, h)
		if len(fields) == 0 {
			if err := co.gb.RestoreHead(ctx, c); err != nil {
				return refuse(hand, err)
			}
			say(handLinePrefix + "no field of " + rowName(c) + " changed in " + c.Abs + "; put the file back as lore wrote it")
			return picked{}, true
		}
		if _, err := st.eng.PickUp(ctx, src, base, h); err != nil {
			return refuse(hand, err)
		}
		return picked{c: c, what: "edited " + rowName(c) + " (" + strings.Join(fields, ", ") + ")", undo: func() error { return gitbackend.PutBack(c, hand) }}, true
	}
}

// pickUpMove picks up a row file moved by hand to another folder: lore's
// move (its own commit), then any field changed on the way as a hand edit.
func (st *state) pickUpMove(ctx context.Context, co *handCheckout, m handMove, say func(string)) {
	from, okFrom := co.places[m.from.Folder]
	to, okTo := co.places[m.to.Folder]
	if !okFrom || !okTo {
		say(handLinePrefix + "move of " + rowName(m.to) + " to " + m.to.Abs + " waits: folder " + m.from.Folder + " or " + m.to.Folder + " of this store is not open in this command; run the move with lore: lore move --id " + m.to.ID.String() + " --to <store>/" + m.to.Folder)
		return
	}
	hand, err := os.ReadFile(m.to.Abs)
	if err != nil {
		say(handRefusal(co.dir, m.to, nil, errs.FileIO("read", m.to.Abs, err)))
		return
	}
	h, err := decodeHand(m.to, hand)
	if err != nil {
		say(handRefusal(co.dir, m.to, hand, err))
		return
	}
	putBack := func() error { return errors.Join(gitbackend.PutBack(m.from, nil), gitbackend.PutBack(m.to, hand)) }
	if err := errors.Join(co.gb.RestoreHead(ctx, m.from), gitbackend.PutBack(m.to, nil)); err != nil {
		say(handRefusal(co.dir, m.to, hand, errors.Join(err, putBack())))
		return
	}
	subject := handMoveSubject + string(m.to.Table) + " " + m.to.ID.String() + " from " + m.from.Folder + " to " + m.to.Folder
	w, err := st.eng.MoveWith(store.WithPersonAuthor(store.WithCommitMessage(ctx, subject)), engine.Ref{Store: from, Table: m.from.Table, ID: m.from.ID}, to, engine.MoveOptions{})
	if err != nil {
		say(handRefusal(co.dir, m.to, hand, errors.Join(err, putBack())))
		return
	}
	say(handLinePrefix + "picked up a move made by hand: " + rowName(m.to) + " from store " + string(from) + " to " + string(to) + ": committed " + shortCommit(co.gb.HeadCommit(ctx)) + " \"" + subject + "\"")
	say("  file " + m.to.Abs)
	fields := engine.PickUpChanged(w.Row, h)
	if len(fields) == 0 {
		return
	}
	edit := handEditSubject + string(m.to.Table) + " " + m.to.ID.String()
	if _, err := st.eng.PickUp(store.WithPersonAuthor(store.WithCommitMessage(ctx, edit)), to, w.Row, h); err != nil {
		say(handRefusal(co.dir, m.to, hand, err))
		return
	}
	say(handLinePrefix + "picked up 1 row edited by hand in store " + string(to) + ": committed " + shortCommit(co.gb.HeadCommit(ctx)) + " \"" + edit + "\" (" + strings.Join(fields, ", ") + ")")
}

// pickUpRepoEdits picks up hand edits of committed rows in a code repo's
// .lore/data (brief 2.7): a row file whose fields differ from the last
// commit while its _hlc is still the committed one (lore's own writes
// always carry a new _hlc), and whose row the engine reads from that file
// (a live row's branch file is shadowed: never touched). It is checked
// and re-stamped through engine.PickUp like a store's hand edit; lore
// never commits in a code repo, so the re-stamped file waits for the
// person's commit. New and deleted files there keep their old handling:
// the repo's pre-commit hook checks new files, and a missing file is
// reported by lore doctor and restored with `lore trash restore
// --from-history`.
func (st *state) pickUpRepoEdits(ctx context.Context, gb *gitbackend.Backend, say func(string)) {
	if gb.GitBusy(ctx) {
		return
	}
	rep, err := gb.HandChanges(ctx)
	if err != nil {
		say(handLinePrefix + "could not look for hand edits in " + gb.Dir() + ": " + err.Error())
		return
	}
	// The files are read, decoded and compared in parallel (B1-16: with a
	// migration not committed yet every row file is modified, ~5,400
	// decodes of the file and of its committed copy on each command); the
	// engine part runs after, one file at a time in git's order, so the
	// lines and writes are the same as a sequential loop.
	cands := make([]repoEditCandidate, len(rep.Rows))
	reg := st.eng.Registry()
	parallelEach(len(rep.Rows), func(i int) { cands[i] = repoEditCandidateOf(reg, rep.Rows[i]) })
	for i, c := range rep.Rows {
		cand := cands[i]
		if cand.refused != nil {
			say(handRefusal(gb.Dir(), c, cand.hand, cand.refused))
			continue
		}
		if len(cand.fields) == 0 {
			continue
		}
		_, cur, gerr := st.eng.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: c.Table, ID: c.ID})
		if gerr != nil || cur.HLC != cand.h.HLC {
			continue // the engine reads this row elsewhere (a live row)
		}
		if _, err := st.eng.PickUp(ctx, store.SourceRepo, cand.base, cand.h); err != nil {
			say(handRefusal(gb.Dir(), c, cand.hand, err))
			continue
		}
		say(handLinePrefix + "picked up a hand edit of " + rowName(c) + " (" + strings.Join(cand.fields, ", ") + "): checked and stamped with your name and the time in " + c.Abs + "; commit it with your code")
	}
}

// repoEditCandidate is what one modified row file of a code repo holds
// for the pick-up: the fields a person changed by hand (none: nothing to
// pick up), or why the file is refused.
type repoEditCandidate struct {
	hand    []byte
	base, h row.Row
	fields  []string
	refused error
}

// repoEditCandidateOf reads and compares one changed file (no engine
// call, safe to run in parallel).
func repoEditCandidateOf(reg *schema.Registry, c gitbackend.HandChange) repoEditCandidate {
	if c.Kind != gitbackend.HandModified {
		return repoEditCandidate{}
	}
	hand, err := os.ReadFile(c.Abs)
	if err != nil {
		return repoEditCandidate{}
	}
	base, berr := row.Decode(c.Base)
	h, herr := decodeHand(c, hand)
	if berr != nil {
		return repoEditCandidate{}
	}
	if herr != nil {
		if merge3.ContainsConflictMarkers(string(hand)) {
			return repoEditCandidate{} // a merge left for the person: lore sync and the hooks say so
		}
		return repoEditCandidate{hand: hand, refused: herr}
	}
	if h.HLC != base.HLC {
		return repoEditCandidate{} // written by lore (or another branch), not by hand
	}
	if base.SourceVersion != row.FormatVersion {
		// B1-10/B1-16: a v1 file the migration rewrote (not committed
		// yet) keeps its stamp; its field changes are the migration's
		// (empty refs as null, audit defaults), not a hand edit
		base = upcastV1(reg, base)
	}
	// Compare the fields before asking the engine: a format rewrite
	// (`lore fix` migrating every row file) changes thousands of files
	// and no field, and engine.Get of a code-repo row reads the live
	// tree, so asking first made every command after a migration decode
	// the live tree once per changed file (~25 s on 5,400 rows).
	return repoEditCandidate{hand: hand, base: base, h: h, fields: engine.PickUpChanged(base, h)}
}

// parallelEach runs fn(0..n-1) on every CPU and waits for all of them.
func parallelEach(n int, fn func(i int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// rowName is "<table> <id>" for messages.
func rowName(c gitbackend.HandChange) string { return string(c.Table) + " " + c.ID.String() }

// decodeHand reads a hand-edited row file: one JSON object, no conflict
// markers, a row of the table and id its name says.
func decodeHand(c gitbackend.HandChange, hand []byte) (row.Row, error) {
	if merge3.ContainsConflictMarkers(string(hand)) {
		return row.Row{}, errs.New(errs.InvalidInput, "the file holds git conflict markers (<<<<<<<, =======, >>>>>>>)").
			WithHint("keep one side of each conflict and remove the markers")
	}
	h, err := row.Decode(hand)
	if err != nil {
		return row.Row{}, errs.Wrap(errs.InvalidInput, "the file is not a row lore can read: "+err.Error(), err).
			WithHint("a row file is one JSON object; fix it, or put it back with git checkout -- " + c.Path)
	}
	if h.Table != c.Table || h.ID != c.ID {
		return row.Row{}, errs.New(errs.InvalidInput, "the file holds row "+h.ID.String()+" of table "+string(h.Table)+", but its name says "+c.ID.String()+" of table "+string(c.Table)).
			WithHint("put the id back as the file name says; a new row needs a file named after its own id")
	}
	return h, nil
}

// handRefusal is the line of a refused hand edit: file:line, the exact
// reason, and the next step.
func handRefusal(dir string, c gitbackend.HandChange, hand []byte, err error) string {
	reason, hint := err.Error(), ""
	var e *errs.Error
	if errors.As(err, &e) {
		reason, hint = e.Message, e.Hint
		if e.Cause != nil && !strings.Contains(reason, e.Cause.Error()) {
			reason += ": " + e.Cause.Error()
		}
	}
	at := c.Abs
	if hand != nil {
		at += ":" + strconv.Itoa(errorLine(hand, err))
	}
	line := handLinePrefix + "hand edit refused: " + at + ": " + reason
	if hint != "" {
		line += "\n  next: " + hint
	}
	return line + "\n  it is not committed or pushed until the file is fixed (or put back: git -C " + dir + " checkout -- " + c.Path + "); the other edits went ahead"
}

// errorLine is the line of hand an error points at: a JSON syntax error's
// offset, the first conflict marker, else the line of the field the
// message names (the longest field name found in it), else 1.
func errorLine(hand []byte, err error) int {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return lineAt(hand, int(se.Offset))
	}
	if !json.Valid(hand) {
		var v json.RawMessage
		if uerr := json.Unmarshal(hand, &v); errors.As(uerr, &se) {
			return lineAt(hand, int(se.Offset))
		}
	}
	for i, l := range strings.Split(string(hand), "\n") {
		if strings.HasPrefix(l, "<<<<<<<") || strings.HasPrefix(l, "=======") || strings.HasPrefix(l, ">>>>>>>") {
			return i + 1
		}
	}
	msg := err.Error()
	keys := jsonKeys(hand)
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		if len(k) > 1 && strings.Contains(msg, k) {
			if at := bytes.Index(hand, []byte(strconv.Quote(k)+":")); at >= 0 {
				return lineAt(hand, at+1)
			}
			if at := bytes.Index(hand, []byte(strconv.Quote(k))); at >= 0 {
				return lineAt(hand, at+1)
			}
		}
	}
	return 1
}

// lineAt is the 1-based line of byte offset off in b.
func lineAt(b []byte, off int) int {
	if off > len(b) {
		off = len(b)
	}
	if off < 1 {
		return 1
	}
	return bytes.Count(b[:off-1], []byte("\n")) + 1
}

// jsonKeys lists every object key of a JSON document (any depth), in
// order of appearance; nil for a document that is not JSON.
func jsonKeys(b []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(b))
	var keys []string
	// a key is the string token right after '{' or after a value inside
	// an object: track whether the next string token in an object is a key
	type frame struct {
		object bool
		key    bool
	}
	var stack []frame
	for {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		top := len(stack) - 1
		switch v := t.(type) {
		case json.Delim:
			switch v {
			case '{':
				if top >= 0 && stack[top].object {
					stack[top].key = true
				}
				stack = append(stack, frame{object: true, key: true})
			case '[':
				if top >= 0 && stack[top].object {
					stack[top].key = true
				}
				stack = append(stack, frame{})
			default:
				stack = stack[:top]
			}
		case string:
			if top >= 0 && stack[top].object {
				if stack[top].key {
					keys = append(keys, v)
					stack[top].key = false
				} else {
					stack[top].key = true
				}
			}
		default:
			if top >= 0 && stack[top].object {
				stack[top].key = true
			}
		}
	}
}
