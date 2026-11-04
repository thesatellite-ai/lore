// Package git is the git store backend (STORE_SERVER_SPEC 6.1): row files
// in a git repository, in the shared rowfs layout. It has two modes.
//
//   - Store clone mode (OpenClone, and NewOpener for ssh/https/http urls):
//     a bare clone under <home>/clones/<host>/<owner>/<repo>.git plus a
//     worktree lore owns at <home>/clones/<host>/<owner>/<repo>/work. Each
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
	"regexp"
	"strconv"
	"strings"
	"sync"

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

	mu     sync.Mutex
	closed bool
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
// store clone (spec 5.5), none for the code repo.
func (b *Backend) Capabilities() store.Capabilities {
	if b.mode == modeRepo {
		return store.Capabilities{}
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
	if err := b.usable(ctx); err != nil {
		return err
	}
	if b.readOnly {
		return store.ErrReadOnly
	}
	if len(changes) == 0 {
		return nil
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
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	applied, err := b.folder.Apply(ctx, changes)
	if err == nil && b.mode == modeClone {
		err = b.commitBatch(ctx, batch, changes, applied)
	}
	return releaseKeep(err, lock)
}

// WritePurge writes changes like Write and, in the same batch and the same
// commit, adds purged to the folder's _purged.json (rowfs.Folder.ApplyPurge;
// engine.Purger). Repo mode writes files only, as Write does.
func (b *Backend) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	if b.readOnly {
		return store.ErrReadOnly
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	applied, err := b.folder.ApplyPurge(ctx, changes, purged)
	if err == nil && b.mode == modeClone && len(applied.Paths) > 0 {
		subj := changes
		if len(subj) == 0 {
			// only the list changed: name the first purged id in the subject
			subj = []store.Change{{Row: store.Row{ID: purged[0]}}}
		}
		err = b.commitBatch(ctx, Batch{Op: purgeOp}, subj, applied)
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

// subject is the commit subject of a batch: `lore: <op> <first id>`, plus
// how many more rows it holds.
func subject(op string, changes []store.Change) string {
	s := "lore: " + op + " " + changes[0].Row.ID.String()
	if n := len(changes) - 1; n > 0 {
		s += " (+" + strconv.Itoa(n) + " more)"
	}
	return s
}
