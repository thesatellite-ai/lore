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
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
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
	// rosterMu guards rosterFrom: the org roster a store with no org
	// folder is checked against (SetRosterSource, authority.go).
	rosterMu   sync.Mutex
	rosterFrom rosterFunc
	cfg        config
	git        runner
	mode       mode
	readOnly   bool
	// readOnlyErr, when set (MarkReadOnlyBecause), is what a refused write
	// answers instead of the bare store.ErrReadOnly, so the refusal names the
	// store, why it takes no writes and what to do instead (devco N-8).
	readOnlyErr error
	// unwritable: the clone was opened read-only because lore may not
	// write it (openCloneReadOnly, devco:U-4): no fetch, no sync, no lock.
	unwritable bool
	root       *rowfs.Root   // the worktree (clone mode) or the repo top (repo mode)
	folder     *rowfs.Folder // the data folder this Backend reads and writes
	work       string        // absolute worktree path (= root.Dir())
	branch     string        // clone mode: the store branch
	marker     rowfs.Marker  // clone mode: lore-store.yaml at open (zero on an empty store)

	// authorityAt is the file the roster replay is kept in, chosen at open
	// (authorityFileFor: never inside a clone lore does not own); ""
	// looks up the choice of an open of the same clone (authorityPath).
	authorityAt string

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
	// cameBack: a sync cleared the remote-reset mark (remotereset.go).
	cameBack atomic.Bool
	// headsKnown / noHeads: GuardPushGone's answer from the remote itself
	// (remotereset.go remoteHasNoHeads), instead of the tracking refs.
	headsKnown, noHeads bool

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
	// tableRows is the reads narrowed to a few tables (store.WithTables)
	// at tableHead's commit, by table set (tablesKey). Every open lists the
	// small tables (actors, projects) once per helper and, through the
	// engine's move-leftover check, once per other attached store: with four
	// stores a warm lore ask read the same actors table of the same commit
	// about a dozen times (bug hunt H-214). A commit moves HEAD, which
	// misses; it is dropped with the full read (dropCaches).
	tableHead string
	tableRows map[string][]store.Row

	// statusFilesList is the index, info/exclude and the global ignore
	// file, found once (statusstamp.go).
	statusFilesOnce sync.Once
	statusFilesList []string
	// indexPath is the repo's index file, found once (repoStatus).
	indexPathOnce sync.Once
	indexPath     string
	// workPrefix is work's path below the repo's top ("web/", "" at the
	// top), found once: porcelain status paths are made relative to work
	// with it (workRelative).
	workPrefixOnce sync.Once
	workPrefix     string
	workPrefixErr  error
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

// RemoteURL is the store url origin points at ("" when unknown), for a
// message that names where a push did not go.
func (b *Backend) RemoteURL() string { return b.git.remote }

// Branch is the store branch (clone mode; "" in repo mode).
func (b *Backend) Branch() string { return b.branch }

// Marker is lore-store.yaml as read at open (clone mode); zero for an
// empty store and in repo mode.
func (b *Backend) Marker() rowfs.Marker { return b.marker }

// Get implements store.Backend: the row file in the worktree. While a
// person's git operation is in progress in a store clone (a `git pull`
// that stopped on a conflict: lore's merge driver leaves the markers
// inside the JSON values), a row file git lists as unmerged is read as the
// last commit has it (W-10): the conflict is the person's to settle in
// git, and lore shows the row as it was, never the half-merged file.
func (b *Backend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	if err := b.usable(ctx); err != nil {
		return store.Row{}, err
	}
	if b.mode == modeClone {
		path := b.folder.StorePath(table, id)
		if b.unmergedPaths(ctx)[path] {
			return b.rowAtHead(ctx, path, table, id)
		}
		if h, herr := b.headIfHeld(ctx, path); herr == nil && h != "" {
			if auth, _ := b.rowAuthorityOf(ctx, h); auth != nil {
				var r store.Row
				var err error
				if blob, held := auth.heldBlob(path); held {
					r, err = b.rowAtBlob(ctx, blob, table, id)
				} else {
					r, err = b.folder.Get(table, id)
				}
				if err != nil {
					return store.Row{}, err
				}
				return auth.creditRow(path, r), nil // C35: the committer named as the writer
			}
		}
	}
	return b.folder.Get(table, id)
}

// unmergedPaths are the files git lists as unmerged in the clone while a
// person's git operation is in progress (nil otherwise: no git process
// runs when no operation is in progress).
func (b *Backend) unmergedPaths(ctx context.Context) map[string]bool {
	if op, _ := b.personGitOp(ctx, b.work); op == "" {
		return nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-only", "-z", "--diff-filter=U"}})
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, p := range splitZ(out) {
		if p != "" {
			set[p] = true
		}
	}
	return set
}

// rowAtHead is the row file at path (relative to the clone) as HEAD has
// it; store.ErrNotFound when the last commit has none (a file the merge
// brings in).
func (b *Backend) rowAtHead(ctx context.Context, path string, table row.Table, id ids.AnyID) (store.Row, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "blob", "HEAD:" + path}})
	if err != nil {
		return store.Row{}, store.ErrNotFound
	}
	return rowfs.DecodeRowFile(rowfs.RowPath(table, id), out)
}

// rowAtBlob is the row in blob (a git object id); store.ErrNotFound for
// "" (a held-back file with no accepted version, authority.go).
func (b *Backend) rowAtBlob(ctx context.Context, blob string, table row.Table, id ids.AnyID) (store.Row, error) {
	if blob == "" {
		return store.Row{}, store.ErrNotFound
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "blob", blob}})
	if err != nil {
		return store.Row{}, store.ErrNotFound
	}
	return rowfs.DecodeRowFile(rowfs.RowPath(table, id), out)
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
		if rows, next, ok, err := b.readMirrored(ctx, cursor); ok || err != nil {
			return rows, next, err
		}
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
		return b.folder.Missing(ctx, cursor)
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
// `lore doctor`. A file a person's merge left unmerged (W-10) is not one:
// doctor names the merge in progress and the way out instead.
func (b *Backend) Problems(ctx context.Context) ([]rowfs.Problem, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	probs, err := b.folder.Problems(ctx)
	if err != nil || b.mode != modeClone || len(probs) == 0 {
		return probs, err
	}
	unmerged := b.unmergedPaths(ctx)
	if len(unmerged) == 0 {
		return probs, nil
	}
	out := probs[:0]
	for _, p := range probs {
		if !unmerged[path.Join(b.folder.DataPath(), p.Path)] {
			out = append(out, p)
		}
	}
	return out, nil
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
		return b.refusedWrite()
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
	err = b.heldWriteErr(changes, err)
	if err == nil && b.mode == modeClone {
		var undo func()
		if undo, err = b.addMissingMarker(ctx, applied); err == nil {
			if err = b.addMissingReadme(ctx, applied); err == nil {
				err = b.addMissingSignpost(ctx, applied)
			}
			if err == nil {
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
func (b *Backend) addMissingMarker(ctx context.Context, applied *rowfs.Applied) (undo func(), err error) {
	undo = func() {}
	if b.marker.Version > 0 || applied == nil || len(applied.Paths) == 0 {
		return undo, nil
	}
	m, err := rowfs.NewMarker(b.cfg.now())
	if err != nil {
		return undo, err
	}
	if b.cfg.markerOrg != nil {
		// devco:D-721: the org an admin's sync would name later is named
		// in this first commit already
		m.Org, m.OrgStore = b.cfg.markerOrg(ctx, b.git.remote)
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

// addMissingSignpost gives a store clone without the signpost lore 0.1.17
// names (rowfs.SignpostFile) the file, in the same commit as this write,
// as addMissingReadme does README.md: a store moved by an earlier build
// gets it with lore's next commit there. A signpost removed from the files
// only (still in the last commit) is the person's change, kept.
func (b *Backend) addMissingSignpost(ctx context.Context, applied *rowfs.Applied) error {
	if applied == nil || len(applied.Paths) == 0 || rowfs.HasSignpost(b.work) {
		return nil
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "-e", "HEAD:" + rowfs.SignpostFile}}); err == nil {
		return nil
	}
	wrote, err := rowfs.EnsureSignpost(b.work)
	if err != nil || !wrote {
		return err
	}
	applied.AddCreated(filepath.Join(b.work, rowfs.SignpostFile), rowfs.SignpostFile)
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
		return b.refusedWrite()
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
		if undo, err = b.addMissingMarker(ctx, applied); err == nil {
			if err = b.addMissingReadme(ctx, applied); err == nil {
				err = b.addMissingSignpost(ctx, applied)
			}
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
	noun, _, _ := strings.Cut(op, ".")
	own, side := splitSideRows(noun, "", changes)
	n := "1 row"
	if own != 1 {
		n = strconv.Itoa(own) + " rows"
	}
	return "lore: " + t + " (" + n + side + ")"
}

// subject is the commit subject of a batch (B2-4, B3-23): one form for
// every row of every kind, `lore: <op words> <id>`, then the count of the
// command's own rows and the rows written on the side (splitSideRows:
// `lore: task add tsk_… (3 rows)`, `lore: memory add mem_… (+ actor
// act_…)`). The row is the first whose table is the operation's noun,
// else the first that is not an actor copy.
//
// No row text ever goes into a subject: not a body, not a title, not an
// actor's name. A subject is the most visible part of git history (every
// `git log --oneline`, the forge's commit list) and outlives a delete or
// purge, and a title can hold private text as easily as a body. Owner
// decision 3 (2026-11-18) kept rule and memory subjects id-only; devco
// D-685 (owner use pass 4 weak 1) made every kind follow that one rule,
// where a task, plan, project or actor subject still carried its title
// ("lore: task claim tsk_…: Question: …"). A write made outside a command
// is named after its table (`lore: machine register mch_…`); a purge keeps
// `lore: purge <id>`.
func subject(op string, changes []store.Change) string {
	if s, ok := actorOnlySubject(op, changes); ok {
		return s
	}
	c := primaryChange(op, changes)
	words := strings.ReplaceAll(op, ".", " ")
	s := "lore: " + words + " " + c.Row.ID.String()
	if op != DefaultOp && op != purgeOp {
		noun, _, _ := strings.Cut(op, ".")
		own, side := splitSideRows(noun, string(c.Row.Table), changes)
		switch {
		case own > 1:
			s += " (" + strconv.Itoa(own) + " rows" + side + ")"
		case side != "":
			s += " (" + strings.TrimPrefix(side, " ") + ")"
		}
		return s
	}
	if op == DefaultOp {
		// W-11: a write made outside any command (the machine profile, the
		// caller's actor copy, …) is named in words after its table, never
		// "write"; the id stays, no text (B3-23)
		s = "lore: " + backgroundWords(c) + " " + c.Row.ID.String()
		if c.Row.Deleted {
			s = "lore: " + singular(string(c.Row.Table)) + " delete " + c.Row.ID.String()
		}
		if len(changes) > 1 {
			s += " (" + strconv.Itoa(len(changes)) + " rows)"
		}
		return s
	}
	if n := len(changes) - 1; n > 0 {
		s += " (+" + strconv.Itoa(n) + " more)"
	}
	return s
}

// actorOnlySubject is the subject of a batch that holds only actor rows
// and none of the command's own (devco:D-642): a member's first write
// registers their agent actor ("<name> via claude_code") in the org
// store, while the row the command wrote went to another store, so that
// store's commit carried just the actor row under "lore: memory add
// act_…", naming a memory that is not in it. It says what the commit is:
// `lore: actor register act_…: <name>` for a new actor row (Expect zero:
// the row must not exist yet), `lore: actor copy act_…: <name>` for a
// refresh. The display name is the row's own short label, already in the
// file; a removal keeps the id only (B3-23). A new actor row written
// outside any command (lore's own registration of an agent at its first
// write) says "actor register" too (devco:D-665), where it read "actor
// copy act_…"; a refresh there keeps W-11's "actor copy <id>". ok is false
// for any other batch, which subject words as before.
func actorOnlySubject(op string, changes []store.Change) (string, bool) {
	if len(changes) == 0 || op == purgeOp {
		return "", false
	}
	noun, _, _ := strings.Cut(op, ".")
	if tableOfNoun(actorsTable, noun) {
		return "", false // an actor command (actor merge, actor add): its own words
	}
	fresh := true
	for _, c := range changes {
		if string(c.Row.Table) != actorsTable || c.Row.Deleted {
			return "", false
		}
		fresh = fresh && c.Expect.IsZero()
	}
	if op == DefaultOp && !fresh {
		return "", false // a refresh outside any command keeps W-11's "actor copy <id>"
	}
	c := changes[0]
	words := actorCopyWords
	if fresh {
		words = actorRegisterWords
	}
	s := "lore: " + words + " " + c.Row.ID.String()
	if len(changes) > 1 {
		s += " (" + strconv.Itoa(len(changes)) + " rows)"
	}
	return s, true
}

// sideRowWords name the rows a write makes on the side of a command's own
// row, per table (devco:D-665): the writer's actor row registered by
// their first write, the folder's project row made at its first write.
var sideRowWords = map[string]string{
	actorsTable:   "actor",
	projectsTable: "project",
}

// projectsTable is the table of a folder's project row.
const projectsTable = "projects"

// maxSideRows bounds the side rows a subject lists by id; the rest are
// counted.
const maxSideRows = 2

// splitSideRows splits a command batch (devco:D-665): own is the number
// of rows that are not side rows, side the subject's words for the side
// rows, " + actor act_…" or " + actor act_…, project prj_…" ("" when
// there are none), which the caller puts in the subject's parentheses:
// `lore: memory add mem_… (+ actor act_…)`, `lore: setup join: <email> (4
// rows + actor act_…)`, where they read "(2 rows)" and "(5 rows)" before.
// A side row is one of sideRowWords' tables that is neither the op noun's
// table nor primary (the subject's own row), so a project add's project
// row and an actor merge's actor rows stay the command's own rows.
func splitSideRows(noun, primary string, changes []store.Change) (int, string) {
	own := 0
	var listed []string
	more := 0
	for _, ch := range changes {
		table := string(ch.Row.Table)
		w, ok := sideRowWords[table]
		if !ok || table == primary || tableOfNoun(table, noun) {
			own++
			continue
		}
		if len(listed) == maxSideRows {
			more++
			continue
		}
		listed = append(listed, w+" "+ch.Row.ID.String())
	}
	if len(listed) == 0 {
		return own, ""
	}
	if more > 0 {
		listed = append(listed, strconv.Itoa(more)+" more")
	}
	return own, " + " + strings.Join(listed, ", ")
}

// Words of an actor-only commit (actorOnlySubject): a new actor row, and a
// refresh of one that exists.
const (
	actorRegisterWords = "actor register"
	actorCopyWords     = "actor copy"
)

// backgroundTitles name the writes lore makes by itself, per table; any
// other table is "<noun> save".
var backgroundTitles = map[string]string{
	"machines": "machine register",
	"actors":   actorCopyWords,
}

// backgroundWords is the words of a write made outside a command.
func backgroundWords(c store.Change) string {
	if w, ok := backgroundTitles[string(c.Row.Table)]; ok {
		return w
	}
	return singular(string(c.Row.Table)) + " save"
}

// singular is a table name as one noun with spaces (memories → memory,
// task_lists → task list).
func singular(table string) string {
	n := strings.ReplaceAll(table, "_", " ")
	switch {
	case strings.HasSuffix(n, "ies"):
		return strings.TrimSuffix(n, "ies") + "y"
	case strings.HasSuffix(n, "ses"):
		return strings.TrimSuffix(n, "es")
	case strings.HasSuffix(n, "s"):
		return strings.TrimSuffix(n, "s")
	}
	return n
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
