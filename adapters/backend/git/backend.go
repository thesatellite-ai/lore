// Package git is the git store backend (STORE_SERVER_SPEC 6.1): row files
// in a git repository, in the shared rowfs layout. It has two modes.
//
//   - Store clone mode (OpenClone, and NewOpener for ssh/https/http urls):
//     a normal git clone at <home>/stores/<owner>/<repo> (or a person's
//     own clone, `lore store use`), lore's lock and stamps in its
//     .git/lore folder (clonelayout.go). Each
//     Write is one commit; Sync fetches, merges (rows both sides changed go
//     through the injected Merger) and pushes, retrying a lost push race.
//   - Repo mode (OpenRepo): the built-in store `repo`, the code repository
//     itself. Lore writes row files and never commits: the person commits
//     them with their code. Sync does nothing.
//
// One Backend opens one data folder of the store (owner decision O1): the
// root folder by default, a project folder or `org` with `?folder=<name>`
// on the url (rowfs.FolderParam) or the folder argument of OpenClone and
// OpenRepo. Folders of one clone share its worktree, lock and commits.
//
// Layer L3 (adapters): imports model, store and adapters/backend/rowfs; git
// itself is an external program (DefaultBinary), never a Go dependency.
package git

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// mode is the backend's mode.
type mode int

const (
	modeClone mode = iota // a store clone lore owns and commits to
	modeRepo              // the code repo; files only, never a commit
)

// Backend is one open data folder of a git store.
type Backend struct {
	cfg      config
	git      runner
	mode     mode
	readOnly bool
	root     *rowfs.Root   // the worktree (clone mode) or the repo top (repo mode)
	folder   *rowfs.Folder // the data folder this Backend reads and writes
	work     string        // absolute worktree path (= root.Dir())
	branch   string        // clone mode: the store branch
	marker   rowfs.Marker  // clone mode: lore-store.yaml at open (zero on an empty store)

	// beforePush, when set (tests only), runs before each push attempt so a
	// test can push from another clone and make this one lose the race.
	beforePush func(attempt int)

	// openScope is the FetchScope this Backend's first open fetched every
	// ref under (nil = no scope, no first-open fetch, or a push ended its
	// reuse); openLive marks that the fetch copied live refs (openFetch).
	openScope atomic.Pointer[FetchScope]
	openLive  bool

	mu     sync.Mutex
	closed bool

	// pushes are the pushes not yet taken by the caller (pushlog.go).
	pushes pushLog

	// handSetUp lists what this open set up in the clone for plain git
	// (handgit.go), for wire to say once.
	handSetUp []string
	// handPending: the open left the plain-git set-up for later (a read
	// never writes it, owner bug 6); the first write or an explicit
	// setup or sync does it (setUpHandGitOnce).
	handPending bool
	// identitySet is the identity this open wrote into the clone's
	// .git/config (cloneidentity.go); zero when it wrote none.
	identitySet CloneIdentity
	// identityRemote is the remote of an open that left the clone's
	// identity for its first write or an explicit sync (a read writes
	// none, owner bug 6); "" when nothing waits (setUpHandGitOnce).
	identityRemote string

	// full is the last full read of the folder (clone mode, Read with no
	// commit cursor) and the HEAD commit it was read at. A full read lists
	// and reads every row file at HEAD, and one command asks for it once
	// per table it lists (engine List), ~100 times for a `task add` with
	// org and team stores: the same commit gives the same rows, so a read
	// at an unchanged HEAD is answered from here instead of two git runs
	// (ls-tree, cat-file) each time. A commit moves HEAD, which misses.
	fullMu   sync.Mutex
	fullHead string
	fullRows []store.Row
}

var _ store.Backend = (*Backend)(nil)

// usable fails after Close or when ctx is done.
func (b *Backend) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errs.Wrap(errs.Internal, "git: closed", store.ErrClosed)
	}
	return nil
}

// Dir is the worktree (clone mode) or repository top (repo mode).
func (b *Backend) Dir() string { return b.work }

// Branch is the store branch (clone mode; "" in repo mode).
func (b *Backend) Branch() string { return b.branch }

// Marker is lore-store.yaml as read at open (clone mode); zero for an
// empty store and in repo mode.
func (b *Backend) Marker() rowfs.Marker { return b.marker }

// Get implements store.Backend: the row file in the worktree.
func (b *Backend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	if err := b.usable(ctx); err != nil {
		return store.Row{}, err
	}
	return b.folder.Get(table, id)
}

// Read implements store.Backend. Clone mode: the cursor is a commit id and
// Read returns the rows of the folder that changed between it and HEAD.
// Repo mode: the stat cursor of rowfs, which also sees files changed but
// not committed (lore never commits there).
func (b *Backend) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	if err := b.usable(ctx); err != nil {
		return nil, "", err
	}
	if b.mode == modeRepo {
		return b.folder.Read(ctx, cursor)
	}
	return b.readCommits(ctx, cursor)
}

// Capabilities implements store.Backend: History, Review and Remote for a
// store clone (spec 5.5); History only for the code repo, where row history
// reads the repo's own git log (spec 5.5, OVERNIGHT Qs 2026-11-05).
func (b *Backend) Capabilities() store.Capabilities {
	if b.mode == modeRepo {
		return store.Capabilities{History: true}
	}
	return store.Capabilities{History: true, Review: true, Remote: true}
}

// Close implements store.Backend; idempotent.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

// Missing lists row files gone without a tombstone (paths relative to the
// data folder), for `lore doctor`; never a delete. Repo mode compares the
// stat cursor with the disk; clone mode lists files of the folder that
// exist at the cursor commit but not at HEAD (cursor ignored when it is
// not a commit: nothing is missing then).
func (b *Backend) Missing(ctx context.Context, cursor store.Cursor) ([]string, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	if b.mode == modeRepo {
		return b.folder.Missing(cursor)
	}
	return b.missingCommits(ctx, cursor)
}

// ReadProblems lists the row files the last Read left out as unusable
// (rowfs.Folder.ReadProblems), so a command can say so instead of showing
// a silent gap. Repo mode only: a store clone reads commits, and its
// broken files are reported by Problems (lore doctor).
func (b *Backend) ReadProblems() []rowfs.Problem {
	if b.mode != modeRepo {
		return nil
	}
	return b.folder.ReadProblems()
}

// Problems lists data-folder entries that are not usable rows; for
// `lore doctor`.
func (b *Backend) Problems(ctx context.Context) ([]rowfs.Problem, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	return b.folder.Problems(ctx)
}

// Batch names one write for its commit (clone mode): the operation that
// made it and, optionally, the actor it is written for. It lives here, not
// on store.Change, because only a backend that records history needs it.
type Batch struct {
	// Op is the operation name in the commit subject `lore: <op> <id>`
	// ("memory.add"); empty means DefaultOp.
	Op string
	// Author signs the commit; zero means the opener's author.
	Author Signature
}

// DefaultOp is the commit operation of a plain Write.
const DefaultOp = "write"

// opPattern is a valid operation name: the op registry's dotted
// snake_case names, nothing that could break the subject line.
var opPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// maxOpLen bounds an operation name in a subject.
const maxOpLen = 64

// Write implements store.Backend: WriteBatch with DefaultOp.
func (b *Backend) Write(ctx context.Context, changes []store.Change) error {
	return b.WriteBatch(ctx, Batch{}, changes)
}

// WriteBatch writes checked changes all or none under the store lock.
// Clone mode then stages them and makes ONE commit for the batch; if the
// commit fails the files are restored. Repo mode writes files only.
func (b *Backend) WriteBatch(ctx context.Context, batch Batch, changes []store.Change) error {
	return b.writeBatch(ctx, batch, changes, nil)
}

var _ store.AuditJournal = (*Backend)(nil)

// WriteAudited implements store.AuditJournal (CLEANUP Qs): the rows and
// their audit records in one batch. Clone mode: the records go to the
// pending journal outside the worktree and the rows into ONE commit; a
// failed commit restores the files and the journal. Repo mode: files and
// journal (.lore/state, git-ignored), all or none.
func (b *Backend) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	return b.writeBatch(ctx, Batch{}, changes, recs)
}

// PendingAudit implements store.AuditJournal.
func (b *Backend) PendingAudit(ctx context.Context) ([]store.AuditRecord, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	return b.root.PendingJournal()
}

// AckAudit implements store.AuditJournal.
func (b *Backend) AckAudit(ctx context.Context, recs []store.AuditRecord) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	return b.root.AckJournal(ctx, recs)
}

// writeBatch is WriteBatch with optional audit records (WriteAudited).
func (b *Backend) writeBatch(ctx context.Context, batch Batch, changes []store.Change, recs []store.AuditRecord) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	if b.readOnly {
		return store.ErrReadOnly
	}
	if len(changes) == 0 {
		return nil
	}
	if o := store.CommitOp(ctx); batch.Op == "" && len(o) <= maxOpLen && opPattern.MatchString(o) {
		batch.Op = o // the running operation names the commit (B2-4)
	}
	if batch.Op == "" {
		batch.Op = DefaultOp
	}
	if len(batch.Op) > maxOpLen || !opPattern.MatchString(batch.Op) {
		return errs.New(errs.InvalidInput, "git: the batch operation name must be dotted snake_case")
	}
	if strings.ContainsAny(batch.Author.Name+batch.Author.Email, "\n\r<>") {
		return errs.New(errs.InvalidInput, "git: the batch author has a forbidden character")
	}
	if b.mode == modeClone {
		if err := b.refusePersonGitOp(ctx, b.work); err != nil {
			return err // B2-9: never write over a person's merge or rebase
		}
		// a write to the store clone is the first point that may set it up
		// for plain git (owner bug 6): said before the commit is made
		b.setUpHandGitOnce(ctx)
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	applied, err := b.folder.ApplyAudited(ctx, changes, recs)
	if err == nil && b.mode == modeClone {
		var undo func()
		if undo, err = b.addMissingMarker(applied); err == nil {
			if err = b.addMissingReadme(ctx, applied); err == nil {
				err = b.commitBatch(ctx, batch, changes, applied)
			}
			if err != nil {
				undo()
			}
		}
	}
	return releaseKeep(err, lock)
}

// addMissingMarker gives a store clone that has no marker yet (a brand-new
// empty repo, or one adopted holding only lore folder files) the marker
// `lore store init` writes, in the same commit as this first write: a
// store must never hold lore rows without it (every later open would
// refuse it as "not a lore store"). It runs under the store lock; the
// marker joins applied's paths, so a failed commit removes the file again
// (Undo), and the returned undo forgets it on the handle.
func (b *Backend) addMissingMarker(applied *rowfs.Applied) (undo func(), err error) {
	undo = func() {}
	if b.marker.Version > 0 || applied == nil || len(applied.Paths) == 0 {
		return undo, nil
	}
	m, err := rowfs.NewMarker(b.cfg.now())
	if err != nil {
		return undo, err
	}
	if err = rowfs.WriteMarker(b.work, m); err != nil {
		return undo, err
	}
	applied.AddCreated(filepath.Join(b.work, rowfs.MarkerFile), rowfs.MarkerFile)
	old := b.marker
	b.marker = m
	return func() { b.marker = old }, nil
}

// addMissingReadme gives a store clone that has no README.md (neither in
// its last commit nor in its files) lore's README (rowfs.StoreReadme,
// D-f), in the same commit as this write: written once, with lore's next
// commit in the store, never by a read and never over a person's own
// README or their removal of it from the files only. A README that is
// byte for byte an earlier lore's text (rowfs.ReadmeOutdated) is replaced
// with the current one the same way. The file joins applied's paths, so a
// failed commit removes it again, or writes the earlier text back (Undo).
func (b *Backend) addMissingReadme(ctx context.Context, applied *rowfs.Applied) error {
	if applied == nil || len(applied.Paths) == 0 {
		return nil
	}
	full := filepath.Join(b.work, rowfs.ReadmeFile)
	if rowfs.HasReadme(b.work) {
		if !rowfs.ReadmeOutdated(b.work) {
			return nil
		}
		prev, err := os.ReadFile(full)
		if err != nil {
			return errs.FileIO("read", full, err)
		}
		if _, err = rowfs.EnsureReadme(b.work); err != nil {
			return err
		}
		applied.AddReplaced(full, rowfs.ReadmeFile, prev)
		return nil
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "-e", "HEAD:" + rowfs.ReadmeFile}}); err == nil {
		return nil // in the last commit, gone from the files: the person's change, kept
	}
	wrote, err := rowfs.EnsureReadme(b.work)
	if err != nil || !wrote {
		return err
	}
	applied.AddCreated(full, rowfs.ReadmeFile)
	return nil
}

// WritePurge writes changes like Write and, in the same batch and the same
// commit, adds purged to the folder's _purged.json (rowfs.Folder.ApplyPurge;
// engine.Purger). Repo mode writes files only, as Write does.
func (b *Backend) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	return b.writePurge(ctx, changes, purged, nil)
}

var _ store.PurgeAuditJournal = (*Backend)(nil)

// WritePurgeAudited implements store.PurgeAuditJournal (later 542): the
// purge's tombstones, its _purged.json ids and its audit records in one
// batch. Clone mode: the records go to the pending journal outside the
// worktree and the files into the purge's ONE commit; a failed commit
// restores the files and the journal. Repo mode: files and journal, all
// or none.
func (b *Backend) WritePurgeAudited(ctx context.Context, changes []store.Change, purged []ids.AnyID, recs []store.AuditRecord) error {
	return b.writePurge(ctx, changes, purged, recs)
}

// writePurge is WritePurge with optional audit records (WritePurgeAudited).
func (b *Backend) writePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID, recs []store.AuditRecord) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	if b.readOnly {
		return store.ErrReadOnly
	}
	if b.mode == modeClone {
		if err := b.refusePersonGitOp(ctx, b.work); err != nil {
			return err // B2-9
		}
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	applied, err := b.folder.ApplyPurgeAudited(ctx, changes, purged, recs)
	undo := func() {}
	if err == nil && b.mode == modeClone && len(applied.Paths) > 0 {
		if undo, err = b.addMissingMarker(applied); err == nil {
			err = b.addMissingReadme(ctx, applied)
		}
	}
	if err == nil && b.mode == modeClone && len(applied.Paths) > 0 {
		subj := changes
		if len(subj) == 0 {
			// only the list changed: name the first purged id in the subject
			subj = []store.Change{{Row: store.Row{ID: purged[0]}}}
		}
		if err = b.commitBatch(ctx, Batch{Op: purgeOp}, subj, applied); err != nil {
			undo()
		}
	}
	return releaseKeep(err, lock)
}

// purgeOp is the batch operation of a purge commit ("lore: purge <id>").
const purgeOp = "purge"

// Purged returns the ids the folder's _purged.json lists (engine.Purger).
func (b *Backend) Purged(ctx context.Context) (map[string]bool, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	return b.folder.Purged()
}

// releaseKeep releases lock and keeps the first error.
func releaseKeep(err error, lock *rowfs.Lock) error {
	if rerr := lock.Release(); rerr != nil && err == nil {
		return errs.Wrap(errs.Internal, "git: release the store lock", rerr)
	}
	return err
}

// maxTitleLen bounds a bulk write's title in a subject (store.CommitTitle).
const maxTitleLen = 120

// subjectOf is the commit subject of a batch: a bulk write's title when
// ctx carries one (store.WithCommitTitle: `lore: actor merge act_… into
// act_… (66 rows)`), else subject.
func subjectOf(ctx context.Context, op string, changes []store.Change) string {
	t := strings.TrimSpace(store.CommitTitle(ctx))
	if t == "" || strings.ContainsAny(t, "\n\r") {
		return subject(op, changes)
	}
	if len(t) > maxTitleLen {
		t = strings.ToValidUTF8(t[:maxTitleLen], "")
	}
	n := "1 row"
	if len(changes) != 1 {
		n = strconv.Itoa(len(changes)) + " rows"
	}
	return "lore: " + t + " (" + n + ")"
}

// subject is the commit subject of a batch (B2-4, B3-23): `lore: <op
// words>: <title>` for a row with a short title (a task, a plan: `lore:
// task add: Ship the beta (3 rows)`), else `lore: <op words> <id>`. The
// row is the first whose table is the operation's noun, else the first
// that is not an actor copy. Free text (a memory's or rule's body, a
// comment) never goes into a subject: a subject is the most visible part
// of git history (every `git log --oneline`, the forge's commit list) and
// outlives a delete or purge. A delete, trash or purge subject names only
// the operation and the id, and a title the secret scanner flags falls
// back to the id too. A plain write or purge keeps `lore: <op> <id>`.
func subject(op string, changes []store.Change) string {
	c := primaryChange(op, changes)
	words := strings.ReplaceAll(op, ".", " ")
	s := "lore: " + words + " " + c.Row.ID.String()
	if op != DefaultOp && op != purgeOp {
		if l := rowTitle(c.Row); l != "" && !removingOp(op) && !looksSecret(l) {
			s = "lore: " + words + ": " + l
		}
		if len(changes) > 1 {
			return s + " (" + strconv.Itoa(len(changes)) + " rows)"
		}
		return s
	}
	if n := len(changes) - 1; n > 0 {
		s += " (+" + strconv.Itoa(n) + " more)"
	}
	return s
}

// removingVerbs are the operation words that take a row away; their
// subjects carry no text at all (B3-23), only the operation and the id.
var removingVerbs = map[string]bool{"delete": true, "purge": true, "trash": true, "remove": true, "forget": true, "archive": true, "merge": true}

// removingOp reports whether op takes rows away (any dotted segment is a
// removing verb: memory.delete, sync.purge, trash.restore is not).
func removingOp(op string) bool {
	for _, seg := range strings.Split(op, ".") {
		if removingVerbs[seg] {
			return true
		}
	}
	return false
}

// actorsTable is the table of the actor copy a write may carry.
const actorsTable = "actors"

// primaryChange is the change a batch's subject names (see subject).
func primaryChange(op string, changes []store.Change) store.Change {
	noun, _, _ := strings.Cut(op, ".")
	for _, c := range changes {
		if tableOfNoun(string(c.Row.Table), noun) {
			return c
		}
	}
	for _, c := range changes {
		if string(c.Row.Table) != actorsTable {
			return c
		}
	}
	return changes[0]
}

// tableOfNoun reports whether table is the plural of noun (task → tasks,
// memory → memories, policy → policies).
func tableOfNoun(table, noun string) bool {
	if noun == "" {
		return false
	}
	if table == noun || table == noun+"s" || table == noun+"es" {
		return true
	}
	stem, ok := strings.CutSuffix(noun, "y")
	return ok && table == stem+"ies"
}

// titleFields are the short title fields a subject may show, per table
// ("" = any table): a title, and a task list's name. Never a body,
// content, description or text field (B3-23).
var titleFields = []struct{ table, field string }{{"", "title"}, {"task_lists", "name"}}

// maxLabelLen bounds a title in a subject, in runes.
const maxLabelLen = 60

// rowTitle is the short title of a row: its title field's first line,
// shortened; "" when its kind has none, the row is deleted, or it cannot
// be read.
func rowTitle(r store.Row) string {
	if r.Deleted || len(r.Doc) == 0 {
		return ""
	}
	d, err := r.Decode()
	if err != nil {
		return ""
	}
	for _, tf := range titleFields {
		if tf.table != "" && tf.table != string(r.Table) {
			continue
		}
		v, ok := d.Fields[tf.field]
		if !ok || v == nil || v.Kind() != value.KindText {
			continue
		}
		line, _, _ := strings.Cut(strings.TrimSpace(v.String()), "\n")
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if rs := []rune(line); len(rs) > maxLabelLen {
			line = string(rs[:maxLabelLen-1]) + "…"
		}
		return line
	}
	return ""
}

// subjectScanner is lore's secret scanner (the gitleaks default rules),
// built on the first titled subject; nil when it cannot be built.
var subjectScanner = sync.OnceValue(func() *gitleaks.Scanner {
	rules, err := gitleaks.Defaults()
	if err != nil {
		return nil
	}
	sc, err := gitleaks.New(rules)
	if err != nil {
		return nil
	}
	return sc
})

// looksSecret reports whether the secret scanner flags text; without a
// scanner every title counts as flagged, so no subject carries text the
// scan could not check.
func looksSecret(text string) bool {
	sc := subjectScanner()
	if sc == nil {
		return true
	}
	return len(sc.Scan("title", text)) > 0
}

var _ store.Holder = (*Backend)(nil)

// Hold implements store.Holder: the store lock (the clone's lock beside
// the worktree, or the repo's .lore/lock), reentrant for the returned ctx,
// so WriteBatch, WritePurge and Sync called with it reuse the hold
// (rowfs.Root.Hold; OV21/OV62 meta rebuild).
func (b *Backend) Hold(ctx context.Context) (context.Context, func() error, error) {
	if err := b.usable(ctx); err != nil {
		return ctx, nil, err
	}
	held, lock, err := b.root.Hold(ctx)
	if err != nil {
		return ctx, nil, err
	}
	return held, lock.Release, nil
}
