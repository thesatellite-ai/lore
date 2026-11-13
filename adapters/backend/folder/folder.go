// Package folder is the local-folder store backend (`file:///abs/path`,
// STORE_SERVER_SPEC 6.2): row files in the git layout, no git and no
// network. It exists so every scenario can run without git, so a second
// implementation proves the store interface is real, and for stores that
// stay on one machine.
//
// One Backend opens one data folder of the store (owner decision O1): the
// root folder by default, a project folder or `org` with `?folder=<name>`
// on the url (rowfs.FolderParam) or the folder argument of Open.
//
// Layer L3 (adapters): imports model, store and adapters/backend/rowfs.
package folder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Scheme is the url scheme this backend answers.
const Scheme = "file"

// localHost is the one host a file url may name besides the empty one
// (RFC 8089: file://localhost/path is the same as file:///path).
const localHost = "localhost"

// windowsOS is runtime.GOOS on Windows, where a file url path carries a
// drive letter after its leading slash (file:///C:/stores/x).
const windowsOS = "windows"

// config holds the Opener's options.
type config struct {
	lockTimeout time.Duration
	now         func() time.Time
	stderr      io.Writer // where the store-layout move line goes; nil = os.Stderr
}

// Option configures the folder backend.
type Option func(*config)

// WithLockTimeout sets how long a write waits for the store lock before
// E_STORE_LOCKED (`store.lock_timeout`, default rowfs.DefaultLockTimeout).
func WithLockTimeout(d time.Duration) Option { return func(c *config) { c.lockTimeout = d } }

// WithClock replaces time.Now for the stat cursor (tests).
func WithClock(now func() time.Time) Option { return func(c *config) { c.now = now } }

// WithStderr sets where Open prints the one line that reports a store-layout
// move (default os.Stderr; tests).
func WithStderr(w io.Writer) Option { return func(c *config) { c.stderr = w } }

// NewOpener returns the store.Opener for file:// urls. Register it under
// Scheme in the binary's store.Openers.
func NewOpener(opts ...Option) store.Opener {
	return func(_ context.Context, u *url.URL, o store.OpenOptions) (store.Backend, error) {
		dir, err := PathOf(u)
		if err != nil {
			return nil, err
		}
		name, err := rowfs.FolderOf(u)
		if err != nil {
			return nil, err
		}
		return Open(dir, name, o, opts...)
	}
}

// PathOf returns the absolute folder a file:// url names. A relative or
// host-qualified url (file:relative, file://host/x) is E_STORE_SCHEME
// (spec 6.2): a store must be one unambiguous place on this machine.
func PathOf(u *url.URL) (string, error) {
	if u.Scheme != Scheme {
		return "", errs.Wrap(errs.StoreScheme, "folder: not a file:// url", store.ErrScheme)
	}
	// file://C:/x is the drive path file:///C:/x, not a host named C:
	// (fileuri.FoldDriveHost); folded on a copy, the caller's url is theirs
	c := *u
	u = &c
	fileuri.FoldDriveHost(u)
	if u.Opaque != "" || (u.Host != "" && u.Host != localHost) || u.User != nil {
		return "", errs.New(errs.StoreScheme, "folder: a file url needs an absolute path (file:///abs/path)")
	}
	p := u.Path
	if runtime.GOOS == windowsOS && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) || strings.Contains(u.Path, "/../") || strings.HasSuffix(u.Path, "/..") {
		return "", errs.New(errs.StoreScheme, "folder: a file url needs an absolute path (file:///abs/path)")
	}
	return filepath.Clean(p), nil
}

// Init makes dir a folder store: the folder, lore-store.yaml and the root
// data folder. It refuses a folder that already has a marker
// (E_ALREADY_INITIALIZED), so a store id is never replaced.
// opts are passed to rowfs.Open (rowfs.WithAllowNetworkFS).
func Init(dir string, m rowfs.Marker, opts ...rowfs.Option) error {
	if !filepath.IsAbs(dir) {
		return errs.New(errs.BadPath, "folder: the store path must be absolute")
	}
	if err := os.MkdirAll(dir, storeFolderMode); err != nil {
		return errs.FileIO("create the store folder", dir, err)
	}
	r, err := rowfs.Open(dir, opts...)
	if err != nil {
		return err
	}
	if _, err = rowfs.ReadMarker(r.Dir()); err == nil {
		return errs.New(errs.AlreadyInitialized, "folder: the folder is already a lore store")
	}
	if !errs.Has(err, errs.NotAStore) {
		return err
	}
	if err = rowfs.WriteMarker(r.Dir(), m); err != nil {
		return err
	}
	if _, err = rowfs.EnsureReadme(r.Dir()); err != nil {
		return err
	}
	return ignoreStateDir(r.Dir())
}

// ignoreStateDir keeps the store's own state folder (rowfs.StoreStateDir:
// the lock and the pending audit journal) out of git when the store folder
// is a git work tree's top (D-c): it adds the folder to .gitignore there,
// once. A folder that is no git work tree gets no .gitignore.
func ignoreStateDir(dir string) error {
	if st, err := os.Lstat(filepath.Join(dir, ".git")); err != nil || st == nil {
		return nil
	}
	p := filepath.Join(dir, gitignoreName)
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.FileIO("read", p, err)
	}
	line := "/" + rowfs.StoreStateDir + "/"
	for l := range strings.SplitSeq(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte("# lore's lock and pending audit journal for this store (never committed)\n"+line+"\n")...)
	if err := os.WriteFile(p, b, storeFileMode); err != nil {
		return errs.FileIO("write", p, err)
	}
	return nil
}

// gitignoreName is git's ignore file.
const gitignoreName = ".gitignore"

// storeFolderMode is the mode of the store folder itself and any missing
// parent, made when the person chose "create" on a folder that did not
// exist yet (0755, as a folder they made with mkdir).
const (
	storeFolderMode = 0o755
	// storeFileMode is a .gitignore Init writes into a person's work tree.
	storeFileMode = 0o644
)

// Backend is one open data folder of a folder store.
type Backend struct {
	root     *rowfs.Root
	folder   *rowfs.Folder
	marker   rowfs.Marker
	readOnly bool

	mu     sync.Mutex
	closed bool
}

var _ store.Backend = (*Backend)(nil)

// Open opens the data folder name of the store at dir. dir must hold
// lore-store.yaml (else store.ErrNotAStore) and be on a local file system
// (else E_NETWORK_FS). A store of the first plain layout (lore_store: 3)
// opened for writing has its root folder's files moved into _store/ first
// (rowfs.MoveRootToStoreDir).
func Open(dir, name string, o store.OpenOptions, opts ...Option) (*Backend, error) {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	r, err := rowfs.Open(dir, rowfs.WithLockTimeout(c.lockTimeout), rowfs.WithLockTimeout(o.LockTimeout), rowfs.WithClock(c.now), rowfs.WithAllowNetworkFS(o.AllowNetworkFS))
	if err != nil {
		return nil, err
	}
	if !o.ReadOnly && rowfs.NeedsStoreDirMove(r.Dir()) {
		// a store of the first plain layout: its root folder's rows move
		// from the top into _store/ before anything reads them (D-ad;
		// rowfs.MoveRootToStoreDir, under the store lock)
		mv, err := r.MoveRootToStoreDir(context.Background())
		if err != nil {
			return nil, err
		}
		if ch := mv.Change(); !ch.Empty() {
			w := c.stderr
			if w == nil {
				w = os.Stderr
			}
			_, _ = fmt.Fprintln(w, StoreDirMoveLine(filepath.Base(r.Dir()), ch))
		}
	}
	m, err := rowfs.ReadMarker(r.Dir())
	if err != nil {
		return nil, err
	}
	f, err := r.Folder(name)
	if err != nil {
		return nil, err
	}
	return &Backend{root: r, folder: f, marker: m, readOnly: o.ReadOnly}, nil
}

// StoreDirMoveLine is the stderr line Open prints after moving a folder
// store's own rows into _store/ (the folder twin of the git layout commit's
// line): what changed, by name, never a zero count — "lore: store x: layout
// change — 12 rows moved to _store/, marker lore_store: 4".
func StoreDirMoveLine(name string, c rowfs.LayoutChange) string {
	return fmt.Sprintf("lore: store %s: layout change — %s", name, c.Summary())
}

// Marker is the store's lore-store.yaml as read at Open.
func (b *Backend) Marker() rowfs.Marker { return b.marker }

// usable fails after Close or when ctx is done.
func (b *Backend) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errs.Wrap(errs.Internal, "folder: closed", store.ErrClosed)
	}
	return nil
}

// Read implements store.Backend with the stat cursor (rowfs.StatCursor).
func (b *Backend) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	if err := b.usable(ctx); err != nil {
		return nil, "", err
	}
	return b.folder.Read(ctx, cursor)
}

// Get implements store.Backend.
func (b *Backend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	if err := b.usable(ctx); err != nil {
		return store.Row{}, err
	}
	return b.folder.Get(table, id)
}

// Write implements store.Backend: under the store lock, every Expect is
// checked against disk, then every file is written; a failure part way
// restores the files already replaced.
func (b *Backend) Write(ctx context.Context, changes []store.Change) error {
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
	_, err = b.folder.Apply(ctx, changes)
	return joinRelease(err, lock)
}

// WritePurge writes changes like Write and, in the same batch, adds purged
// to the folder's _purged.json (rowfs.Folder.ApplyPurge): the engine's
// purge path (engine.Purger).
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
	_, err = b.folder.ApplyPurge(ctx, changes, purged)
	return joinRelease(err, lock)
}

// Purged returns the ids the folder's _purged.json lists (engine.Purger).
func (b *Backend) Purged(ctx context.Context) (map[string]bool, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	return b.folder.Purged()
}

// joinRelease releases lock and keeps the first error.
func joinRelease(err error, lock *rowfs.Lock) error {
	if rerr := lock.Release(); rerr != nil && err == nil {
		return errs.Wrap(errs.Internal, "folder: release the store lock", rerr)
	}
	return err
}

// Sync implements store.Backend: a folder has no remote.
func (b *Backend) Sync(ctx context.Context) (store.SyncReport, error) {
	if err := b.usable(ctx); err != nil {
		return store.SyncReport{}, err
	}
	return store.SyncReport{}, nil
}

// Capabilities implements store.Backend: none (spec 5.5).
func (b *Backend) Capabilities() store.Capabilities { return store.Capabilities{} }

// Close implements store.Backend; idempotent.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

// Missing lists row files the cursor saw that are gone without a tombstone
// (paths relative to the data folder). Never a delete; for `lore doctor`.
func (b *Backend) Missing(ctx context.Context, cursor store.Cursor) ([]string, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	return b.folder.Missing(cursor)
}

// ReadProblems lists the row files the last Read left out as unusable
// (rowfs.Folder.ReadProblems), so a command can say so instead of showing
// a silent gap.
func (b *Backend) ReadProblems() []rowfs.Problem { return b.folder.ReadProblems() }

// Problems lists data-folder entries that are not usable rows; for
// `lore doctor`.
func (b *Backend) Problems(ctx context.Context) ([]rowfs.Problem, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	return b.folder.Problems(ctx)
}

var _ store.Holder = (*Backend)(nil)

// Hold implements store.Holder: the store lock, reentrant for the
// returned ctx (rowfs.Root.Hold; OV21/OV62 meta rebuild).
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

var _ store.AuditJournal = (*Backend)(nil)

// WriteAudited implements store.AuditJournal (CLEANUP Qs): under the store
// lock, the audit records go to the pending journal
// (<store>/.lore/state/audit-pending.jsonl, machine-local) and the rows to
// their files, all or none (rowfs.Folder.ApplyAudited).
func (b *Backend) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
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
	_, err = b.folder.ApplyAudited(ctx, changes, recs)
	return joinRelease(err, lock)
}

var _ store.PurgeAuditJournal = (*Backend)(nil)

// WritePurgeAudited implements store.PurgeAuditJournal (later 542): under
// the store lock, the purge's tombstones, its _purged.json ids and its
// audit records (pending journal), all or none
// (rowfs.Folder.ApplyPurgeAudited).
func (b *Backend) WritePurgeAudited(ctx context.Context, changes []store.Change, purged []ids.AnyID, recs []store.AuditRecord) error {
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
	_, err = b.folder.ApplyPurgeAudited(ctx, changes, purged, recs)
	return joinRelease(err, lock)
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
