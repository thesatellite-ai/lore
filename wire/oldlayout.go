package wire

// oldlayout.go — the first run of this lore on a machine that ran an
// earlier 0.1.18 build (plain files round, brief 1.6). Before the command
// runs, lore moves what that build left into the plain layout, by itself,
// and says what it did:
//
//	1. clone move   every old clone (~/.lore/clones/<host>/<owner>/<repo>.git
//	                + <repo>/work) becomes a plain clone at
//	                ~/.lore/stores/<owner>/<repo> (git.MoveOldClone): commits
//	                the remote lacks and uncommitted changes carried over,
//	                no network needed; the old clone is left in place
//	2. sync         each moved clone syncs per its store's push mode: its
//	                carried commits are pushed now when the store pushes now
//	                and the remote answers; otherwise they wait, as any
//	                local commit does, for the next push
//	3. flatten      every store clone whose tree is still in the old layout
//	                (rows under <folder>/.lore/data) gets one commit moving
//	                them under <folder>/ (the root folder's into _store/,
//	                git.Backend.FlattenLayout), pushed per the push mode
//	                with the push line printed; a store of the first plain
//	                layout (lore_store: 3, the root folder's tables at the
//	                store's top) gets one commit moving those into _store/
//	                (D-ad), pushed the same way
//
// A v0.1.17 store is moved by the run that migrates it instead (lore fix,
// lore migrate run: FlattenAfterMigrate), in that same command.
//
// Every step is idempotent: a second run finds nothing to do and prints
// nothing (the check is a scan of two folders, no git process). A run cut
// short is finished by the next one. The old clones-v017 folder is never
// moved (the org link code uses the store clone now); it is named once.
// `lore doctor` (doctor_oldclones.go) says when an old clone may go.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
)

// oldLayoutPrefix starts every line of the first-run step.
const oldLayoutPrefix = "lore: "

// MoveOldLayout runs the first-run step (see the file comment), writing
// what it did to w. It never fails the command: a clone it cannot move
// or flatten is named with the cause and the next step, and everything
// else goes on. A machine with nothing to move costs two folder scans.
func (a *App) MoveOldLayout(ctx context.Context, w io.Writer) {
	home := a.opts.Home
	if home == "" {
		return
	}
	moveNeeded := git.MoveNeeded(home)
	flat := flattenCandidates(home)
	personal := personalV017Candidates(home)
	if !moveNeeded && len(flat) == 0 && len(personal) == 0 {
		return
	}
	say := func(line string) { _, _ = fmt.Fprintln(w, oldLayoutPrefix+line) }
	moved := map[string]bool{}
	if moveNeeded {
		for _, dir := range a.moveOldClones(ctx, home, say) {
			moved[dir] = true
		}
		flat = flattenCandidates(home)
	}
	for dir := range moved {
		if !containsDir(flat, dir) {
			flat = append(flat, dir)
		}
	}
	for _, dir := range flat {
		a.settleClone(ctx, dir, say)
	}
	for _, p := range personalV017Candidates(home) {
		if personalMarkerOld(p.dir) {
			continue // D16: lore migrate run moves it (oldLayoutNotices names it)
		}
		a.flattenPersonal(ctx, p, say)
	}
	a.oldLayoutNotices(home, say)
}

// personalMarkerOld reports a personal store clone whose marker is lore
// 0.1.17's: its everywhere/ notes move only with lore migrate run (D16).
func personalMarkerOld(dir string) bool {
	m, err := rowfs.ReadMarker(dir)
	return err == nil && m.Version < rowfs.MarkerFirstStores
}

// oldLayoutNotices says once per store (a read notice, remembered under
// the lore home) that a store clone is still on lore 0.1.17's layout and
// which command upgrades it (D16): the first-run step no longer does.
func (a *App) oldLayoutNotices(home string, say func(string)) {
	found, err := git.ScanClones(home)
	if err != nil {
		return
	}
	for _, c := range found {
		if c.State != git.CloneWhole || !personalMarkerOld(c.Paths.Dir) {
			continue
		}
		key := "store-layout-v017:" + c.Paths.Dir
		if ReadNoticeSaid(home, key, "1") {
			continue
		}
		say(StoreFormatOldErr(filepath.Base(c.Paths.Dir)).(*errs.Error).Message)
		MarkReadNotice(home, key, "1")
	}
}

// PreviewOldLayout is MoveOldLayout for a dry run: it writes nothing
// (no clone, no move record, no commit) and says what the first command
// that is not a dry run will move. Files only, like MoveOldLayout's check.
func (a *App) PreviewOldLayout(w io.Writer) {
	home := a.opts.Home
	if home == "" {
		return
	}
	say := func(line string) { _, _ = fmt.Fprintln(w, oldLayoutPrefix+line) }
	if git.MoveNeeded(home) {
		found, _ := git.ScanOldClones(home)
		rec := git.ReadMoveRecord(home)
		for _, o := range found {
			if e, ok := rec.Moves[o.Base]; o.Whole && (!ok || git.WorktreeTip(o) != e.Tip) {
				say(fmt.Sprintf("would move store clone %s to %s (dry run: nothing moved; the next command does it)", movedFrom(o), filepath.Join(home, git.StoresDir)))
			}
		}
	}
	for _, dir := range flattenCandidates(home) {
		say(fmt.Sprintf("would give store clone %s its layout commit (dry run: nothing changed)", dir))
	}
	for _, p := range personalV017Candidates(home) {
		say(fmt.Sprintf("would move lore 0.1.17's everywhere/ personal notes in %s to %s/ (dry run: nothing changed)", p.dir, rowfs.StoreDataDir))
	}
}

// personalClone is a git personal store's clone on this machine: its url
// as config.yaml spells it, and the clone folder.
type personalClone struct{ raw, dir string }

// personalV017Candidates are the git personal stores whose clone still
// holds lore 0.1.17's per-org folders (git.PersonalFoldersV017At). Files
// only, no git: a look at each personal clone's top folder.
func personalV017Candidates(home string) []personalClone {
	var out []personalClone
	for _, raw := range personalStoreURLs(home) {
		r, err := git.RemoteOfRaw(raw)
		if err != nil {
			continue // a folder personal store: personal_v017.go handles 0.1.17's
		}
		dir, _, err := git.CloneDirFor(home, r, "")
		if err != nil || !isDirPath(filepath.Join(dir, ".git")) {
			continue
		}
		if git.PersonalEverywhereV017At(dir) {
			out = append(out, personalClone{raw: raw, dir: dir})
		}
	}
	return out
}

// flattenPersonal moves the rows lore 0.1.17 filed under everywhere/ of a
// git personal store to its _store/ folder (`me`, git.Backend.FlattenPersonal;
// <org>/ folders keep their scope, personal_orgs.go), and pushes per the
// push mode.
func (a *App) flattenPersonal(ctx context.Context, p personalClone, say func(string)) {
	name := filepath.Base(p.dir)
	canon, err := canonical(attach.Store{URL: p.raw})
	if err != nil {
		say(fmt.Sprintf("personal store %s holds lore 0.1.17's per-org notes, but its url is not one lore can open: %s", p.dir, errLine(err)))
		return
	}
	b, err := a.openStore(ctx, canon, p.raw)
	if err != nil {
		say(fmt.Sprintf("could not open the personal store clone %s to move lore 0.1.17's notes: %s", p.dir, errLine(err)))
		return
	}
	defer func() { _ = b.Close() }() // the move is committed or not by then
	gb := gitOf(b)
	if gb == nil || gb.Dir() != p.dir {
		return
	}
	a.SayOldLoreLockout(name, p.dir)
	res, err := gb.FlattenPersonal(ctx)
	if err != nil {
		say(fmt.Sprintf("could not move lore 0.1.17's personal notes in %s, left as they are (they are not listed until moved): %s", p.dir, errLine(err)))
		return
	}
	if res.Commit == "" {
		return
	}
	mode := storepush.ModeNow
	if s, serr := storepush.FromRaw(gb.Marker().Push); serr == nil {
		mode = s.Push
	}
	// one line for the move and its push (B4-10: a first read makes this
	// commit, which the docs promise; it says so once)
	var pushLines []string
	a.pushPerMode(ctx, gb, name, mode, "the personal store move", func(l string) { pushLines = append(pushLines, l) })
	pushed := ", not pushed yet"
	if len(pushLines) == 1 && strings.HasPrefix(pushLines[0], "pushed store ") {
		pushed, pushLines = ", pushed", nil
	}
	merged := ""
	if n := len(res.Merged); n > 0 {
		merged = fmt.Sprintf(" (%d found in more than one folder, merged)", n)
	}
	say(fmt.Sprintf("personal store %s: %d row(s) lore 0.1.17 filed under everywhere/ moved to %s/%s: commit %s%s; notes it filed under an org's folder stay there and load only in that org's repos", name, res.Rows, rowfs.StoreDataDir, merged, short(res.Commit), pushed))
	for _, m := range res.Kept {
		say(fmt.Sprintf("personal store %s: %s was in more than one folder and could not be merged; the %s/ copy is kept, the other is in the history (git -C %s log --all -- '*%s')", name, m, rowfs.StoreDataDir, p.dir, filepath.Base(m)))
	}
	for _, l := range pushLines {
		say(l)
	}
}

// FlattenAfterMigrate makes the layout commit of every store clone that
// needs one, in the command that migrated (lore fix, lore migrate run, the
// first write's safe steps), and returns what it did as lines. The
// migration turns a v0.1.17 store's marker into one this lore moves, so
// without this the layout commit (and its push) waited for the next
// command, even a read. Run only when the migration pushes: a --no-push or
// --offline run leaves the move to the next command, which first fetches
// what a teammate pushed. The App's open stores still point at the old
// layout, so the state is closed first and the next use reopens it.
func (a *App) FlattenAfterMigrate(ctx context.Context) []string {
	home := a.opts.Home
	if home == "" {
		return nil
	}
	flat := flattenCandidates(home)
	if len(flat) == 0 && len(personalV017Candidates(home)) == 0 {
		return nil
	}
	_ = a.reopenState() // a close error leaves nothing open that a reopen needs
	var lines []string
	say := func(line string) { lines = append(lines, line) }
	for _, dir := range flat {
		a.settleClone(ctx, dir, say)
	}
	// D16: the personal store's everywhere/ move is lore migrate run's too
	for _, p := range personalV017Candidates(home) {
		if !personalMarkerOld(p.dir) {
			a.flattenPersonal(ctx, p, say)
		}
	}
	return lines
}

// moveOldClones moves every old clone not moved yet (git.MoveOldClone)
// and returns the new clone folders of the ones moved in this run.
func (a *App) moveOldClones(ctx context.Context, home string, say func(string)) []string {
	lock, err := git.MoveLock(ctx, home)
	if err != nil {
		say(fmt.Sprintf("could not move the store clones of an earlier build: %s", errLine(err)))
		return nil
	}
	defer func() { _ = lock.Release() }() // the moves are recorded by then
	found, err := git.ScanOldClones(home)
	if err != nil {
		say(fmt.Sprintf("could not list the store clones of an earlier build in %s: %v; check the folder can be read, then run the command again", filepath.Join(home, git.OldClonesDir), err))
		return nil
	}
	var out, left []string
	for _, o := range found {
		if !o.Whole {
			continue // doctor names it (nothing lore can carry)
		}
		res, merr := git.MoveOldClone(ctx, home, o, a.wall())
		switch {
		case merr != nil:
			say(fmt.Sprintf("could not move the old store clone %s, left as it is: %s", movedFrom(o), errLine(merr)))
		case res.Already:
		case res.To == "":
			say(fmt.Sprintf("old store clone %s had no commit of its own (nothing the remote lacks); nothing to move (the store's next use clones it to %s)", movedFrom(o), filepath.Join(home, git.StoresDir)))
			left = append(left, o.Base)
		default:
			say(fmt.Sprintf("moved store clone %s -> %s%s", movedFrom(o), underHome(res.To), carriedText(res)))
			out = append(out, res.To)
			left = append(left, o.Base)
		}
	}
	if len(left) > 0 {
		say("old clone folders kept; `lore doctor` names how to remove them once you have checked the moves")
		if v017, _ := git.ScanOldOrgClones(home); len(v017) > 0 {
			say(fmt.Sprintf("%s holds %d org link clone(s) of the earlier build; this lore no longer uses them (`lore doctor` names the command that removes them)", filepath.Join(home, git.OldOrgClonesDir), len(v017)))
		}
	}
	return out
}

// movedFrom names an old clone in the first-run lines: its stem folder, or
// for lore 0.1.17's clone the bare clone (the only folder it has), marked
// as 0.1.17's so the person knows which lore left it.
func movedFrom(o git.OldClone) string {
	if o.V017 {
		return underHome(o.Bare) + " (0.1.17)"
	}
	return underHome(o.Base)
}

// carriedText describes what a move carried, in a few words: the local
// commits the remote lacked, the uncommitted files, a merge into a clone
// already there (W-4: one short line per moved clone).
func carriedText(res git.MoveResult) string {
	var parts []string
	if res.Carried > 0 {
		parts = append(parts, plural(res.Carried, "local commit"))
	}
	if len(res.Files) > 0 {
		parts = append(parts, plural(len(res.Files), "local file"))
	}
	if res.Into {
		parts = append(parts, "merged")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (kept " + strings.Join(parts, ", ") + ")"
}

// containsDir reports whether list holds dir.
func containsDir(list []string, dir string) bool {
	for _, d := range list {
		if d == dir {
			return true
		}
	}
	return false
}

// flattenCandidates are the store clones on this machine (under
// ~/.lore/stores and a person's own, `lore store use`) whose files say
// they need the layout commit: rows under a .lore/data folder, or a
// layout commit cut short (git.NeedsFlattenAt). Files only, no git.
func flattenCandidates(home string) []string {
	found, err := git.ScanClones(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, c := range found {
		if c.State != git.CloneWhole {
			continue
		}
		if git.NeedsFlattenAt(c.Paths.Dir) {
			out = append(out, c.Paths.Dir)
		}
	}
	return out
}

// settleClone syncs the clone (a moved clone's carried commits go out when
// the store pushes now; a layout commit already on the remote comes in),
// then gives it the layout commit when it still needs one and pushes per
// the store's push mode.
func (a *App) settleClone(ctx context.Context, dir string, say func(string)) {
	name := filepath.Base(dir)
	origin := git.OriginAt(dir)
	pinned := ""
	if i := strings.LastIndex(name, "@"); i > 0 {
		pinned = name[i+1:]
	}
	canon, err := canonical(attach.Store{URL: origin, Branch: pinned})
	if err != nil {
		say(fmt.Sprintf("store clone %s is in the old layout, but its origin is not a store url lore can open: %s", dir, errLine(err)))
		return
	}
	open := func() (*git.Backend, func(), error) {
		b, oerr := a.openStore(ctx, canon, origin)
		if oerr != nil {
			return nil, nil, oerr
		}
		gb := gitOf(b)
		if gb == nil || gb.Dir() != dir {
			_ = b.Close()
			return nil, nil, errs.New(errs.Internal, "wire: the store at "+origin+" did not open as the clone "+dir).
				WithHint("run lore store list to see which clone lore uses for it")
		}
		return gb, func() { _ = b.Close() }, nil
	}
	gb, done, err := open()
	if err != nil {
		say(fmt.Sprintf("could not open the store clone %s: %s", dir, errLine(err)))
		return
	}
	mode := storepush.ModeNow
	if s, serr := storepush.FromRaw(gb.Marker().Push); serr == nil {
		mode = s.Push
	}
	// first level with the remote: a moved clone's carried commits go out
	// (push mode now), and a layout commit a teammate pushed already is
	// fetched instead of made a second time
	a.syncMoved(ctx, gb, name, mode, say)
	done()
	if gb, done, err = open(); err != nil {
		say(fmt.Sprintf("could not open the store clone %s again: %s", dir, errLine(err)))
		return
	}
	defer done()
	if !gb.NeedsFlatten() {
		return
	}
	if old, oerr := rowfs.IsOldStoreLayout(dir); oerr == nil && old {
		a.SayOldLoreLockout(name, dir)
	}
	res, err := gb.FlattenLayout(ctx)
	if err != nil {
		say(fmt.Sprintf("could not move the rows of store %s to the plain layout, left as it is: %s", name, errLine(err)))
		return
	}
	if res.Commit == "" {
		return
	}
	say(LayoutCommitLine(name, res.Commit, res.Change))
	for _, p := range res.Merged {
		say(fmt.Sprintf("store %s: %s was at both places; merged into one", name, p))
	}
	if len(res.Untracked) > 0 {
		say(fmt.Sprintf("store %s: lore's own files an earlier build committed are no longer tracked: %s", name, strings.Join(res.Untracked, ", ")))
	}
	for _, p := range res.Kept {
		say(fmt.Sprintf("store %s: %s was at both places and could not be merged; the plain-layout copy is kept, the other is in the history (git -C %s log -- %s)", name, p, dir, p))
	}
	a.pushPerMode(ctx, gb, name, mode, "the layout commit", say)
}

// OldLoreLockoutLines are the block said once per store, on its own and
// before the commit, wherever lore makes a commit lore 0.1.17 cannot read
// (SayOldLoreLockout; %s: the store's name). lore 0.1.17 cannot be told
// about the new layout (it checks only that every file sits under a .lore
// folder and calls anything else "not a lore store"), so the person whose
// lore makes the commit is the one who can warn the team.
var OldLoreLockoutLines = []string{
	"lore: store %s moves to the 0.1.18 layout now: anyone who uses it with lore 0.1.17 (a teammate, or another machine of yours) must upgrade lore.",
	"lore:   lore 0.1.17 refuses the new layout with E_NOT_A_STORE (its hint to run lore store init is wrong) on every lore write to this store, and its pre-commit hook refuses git commits in repos linked to an org store; they upgrade and run lore fix.",
}

// lockoutSaid records the clones SayOldLoreLockout named in this process.
var lockoutSaid sync.Map

// lockoutSaidFile, in a store clone's .git/lore, records that this machine
// gave the clone's store the lockout block: a store is moved by up to two
// commits (0.1.17's per-org personal notes on the first command, the
// layout commit on the first write or lore fix), and the block is said
// before the first of them only.
const lockoutSaidFile = "lockout-said"

// SayOldLoreLockout prints OldLoreLockoutLines for store name (its clone
// at dir) to stderr, once per store and machine, right before the first
// commit of that store that 0.1.17 cannot read: the layout commit
// (settleClone: the first command, lore fix, the first write's safe steps)
// or the move of 0.1.17's per-org personal notes (flattenPersonal: the
// first command, a read included). It is printed on its own, never inside
// a step's report, so it is seen before the push line on every path;
// `lore fix --all` passes it through from each repo's run (runFixIn).
// A clone whose state folder cannot be written says it again next time,
// which is the safe side.
func (a *App) SayOldLoreLockout(name, dir string) {
	if _, done := lockoutSaid.LoadOrStore(dir, true); done {
		return
	}
	mark := filepath.Join(git.StateDirOf(dir), lockoutSaidFile)
	if fileExists(mark) {
		return
	}
	w := a.pushNotes()
	for _, l := range OldLoreLockoutLines {
		_, _ = fmt.Fprintln(w, strings.ReplaceAll(l, "%s", name))
	}
	if err := os.MkdirAll(filepath.Dir(mark), 0o700); err == nil {
		_ = os.WriteFile(mark, []byte("lore told this machine's user that lore 0.1.17 users of this store must upgrade\n"), 0o600)
	}
}

// LayoutCommitLine is the line (after the "lore: " prefix) for a store's
// layout commit: what the commit changed, by name, never a zero count —
// "store lore-x: layout commit abc1234 — README refreshed, marker
// lore_store: 4".
func LayoutCommitLine(name, commit string, c rowfs.LayoutChange) string {
	return fmt.Sprintf("store %s: layout commit %s — %s", name, short(commit), c.Summary())
}

// syncMoved brings a clone level with its remote: a store that pushes now
// sends the commits it carries; any other only fetches and merges.
func (a *App) syncMoved(ctx context.Context, gb *git.Backend, name string, mode storepush.Mode, say func(string)) {
	n, _ := gb.Unpushed(ctx)
	if n == 0 || mode != storepush.ModeNow {
		if _, err := gb.SyncWith(ctx, git.SyncOptions{NoPush: true}); err != nil && !errors.Is(err, context.Canceled) {
			say(fmt.Sprintf("store %s: could not fetch its remote (%s); lore sync tries again", name, errLine(err)))
		}
		if n > 0 {
			say(fmt.Sprintf("store %s: %d carried commit(s) wait (push mode %s); lore sync pushes them", name, n, mode))
		}
		return
	}
	a.pushPerMode(ctx, gb, name, mode, strconv.Itoa(n)+" carried commit(s)", say)
}

// pushPerMode pushes what the clone holds when the store pushes now and
// prints the push line; otherwise it says what waits and why.
func (a *App) pushPerMode(ctx context.Context, gb *git.Backend, name string, mode storepush.Mode, what string, say func(string)) {
	if mode != storepush.ModeNow {
		say(fmt.Sprintf("store %s: %s waits (push mode %s); lore sync pushes it", name, what, mode))
		return
	}
	pctx, cancel := context.WithTimeout(ctx, oldLayoutPushTimeout)
	defer cancel()
	rep, err := gb.SyncWith(pctx, git.SyncOptions{})
	left, _ := gb.Unpushed(ctx)
	switch {
	case err != nil:
		say(fmt.Sprintf("store %s: %s not pushed (%s); lore sync pushes it", name, what, errLine(err)))
	case rep.Offline:
		say(fmt.Sprintf("store %s: %s not pushed (the store's remote did not answer); lore sync pushes it", name, what))
	case len(rep.Refused) > 0 || left > 0:
		say(fmt.Sprintf("store %s: %s not pushed (the store refused rows; lore sync status shows which)", name, what))
	default:
		h, _ := gb.Head(ctx)
		say(fmt.Sprintf("pushed store %s: commit %s to branch %s", name, short(h), gb.Branch()))
	}
}

// oldLayoutPushTimeout bounds each push of the first-run step.
const oldLayoutPushTimeout = 2 * time.Minute

// errLine is an error and its hint on one line.
func errLine(err error) string {
	msg := err.Error()
	var e *errs.Error
	if errors.As(err, &e) && e.Hint != "" {
		msg += "; " + e.Hint
	}
	return strings.ReplaceAll(msg, "\n", " ")
}

// wall is the App's clock.
func (a *App) wall() time.Time {
	if a.opts.Wall != nil {
		return a.opts.Wall()
	}
	return time.Now()
}

// OldLayoutSkips reports commands that never run the first-run step: they
// run inside git (merge driver, hooks) or print without touching a store.
func OldLayoutSkips(first string) bool {
	switch first {
	case "", "version", "help", "completion", "__complete", "__completeNoDesc", "merge-driver":
		return true
	}
	return false
}
