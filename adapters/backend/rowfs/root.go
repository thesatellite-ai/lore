package rowfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Root is one store on disk: the folder that holds lore-store.yaml, the
// store lock and the data folders. It is safe for concurrent use.
//
// Invariant: dir is absolute with symlinks resolved, and was checked by
// CheckLocal when the Root was opened unless the caller passed
// WithAllowNetworkFS(true).
type Root struct {
	dir      string
	lockPath string
	lockWait time.Duration
	now      func() time.Time
	// gate serialises this Root's own writers before they touch the OS
	// lock, so goroutines of one process queue instead of polling flock.
	gate chan struct{}
	// allowNetworkFS skips CheckLocal (WithAllowNetworkFS).
	allowNetworkFS bool
	// modes are the modes of the row files and folders it writes
	// (privateModes unless WithSharedModes).
	modes fileModes
	// layout is where its folders' rows sit (LayoutStore unless WithLayout).
	layout Layout
	// journalDir holds the pending audit journal (journal.go).
	journalDir string
}

// Option configures a Root.
type Option func(*Root)

// WithLockTimeout sets how long Lock waits (default DefaultLockTimeout).
func WithLockTimeout(d time.Duration) Option {
	return func(r *Root) {
		if d > 0 {
			r.lockWait = d
		}
	}
}

// WithLockPath puts the lock file somewhere other than the layout's
// default (<root>/.lore-state/lock for a store, <root>/.lore/lock for a
// code repo). The git backend uses it to keep the lock in the clone's
// .git/lore. The journal goes in a "state" folder beside it.
func WithLockPath(path string) Option { return func(r *Root) { r.lockPath = path } }

// WithAllowNetworkFS skips the CheckLocal refusal when allow is true: the
// user's explicit override for a store in a cloud-sync folder or on a
// network file system (store.OpenOptions.AllowNetworkFS). false keeps the
// refusal, so passing the option through unconditionally is safe.
func WithAllowNetworkFS(allow bool) Option { return func(r *Root) { r.allowNetworkFS = allow } }

// WithSharedModes writes row files 0644 and folders 0755 instead of
// owner-only: for a repo checkout, whose .lore/data is committed and read
// by everyone who clones it. Folder stores keep the owner-only default.
func WithSharedModes() Option { return func(r *Root) { r.modes = sharedModes } }

// WithLayout says where the folders' rows sit: LayoutStore (the default:
// a store checkout, rows right under each folder) or LayoutRepo (a code
// repo's .lore/data). Every Open of a code repo passes LayoutRepo.
func WithLayout(l Layout) Option { return func(r *Root) { r.layout = l } }

// WithClock replaces time.Now for the stat cursor (tests, LORE_NOW). Tmp
// cleanup always uses the wall clock: a file's mtime is real time.
func WithClock(now func() time.Time) Option {
	return func(r *Root) {
		if now != nil {
			r.now = now
		}
	}
}

// Open opens the store at dir, which must be an absolute path to an
// existing folder on a local file system. It does not require the marker:
// the folder backend checks that, the git repo mode has none.
func Open(dir string, opts ...Option) (*Root, error) {
	if !filepath.IsAbs(dir) {
		return nil, errs.New(errs.BadPath, "rowfs: the store path must be absolute")
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errs.Wrap(errs.NotAStore, "rowfs: the store folder does not exist", err).
				WithHint("check the store's url (lore store list; personal.store in ~/.lore/config.yaml), or make it with lore store init <name> <url>")
		}
		return nil, errs.Wrap(errs.Internal, "rowfs: resolve the store folder", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: stat the store folder", err)
	}
	if !info.IsDir() {
		return nil, errs.New(errs.NotAStore, "rowfs: the store path is not a folder")
	}
	r := &Root{dir: real, lockWait: DefaultLockTimeout, now: time.Now, gate: make(chan struct{}, 1), modes: privateModes}
	for _, o := range opts {
		o(r)
	}
	if !r.allowNetworkFS {
		if err := CheckLocal(real); err != nil {
			return nil, err
		}
	}
	switch {
	case r.lockPath != "":
		r.journalDir = filepath.Join(filepath.Dir(r.lockPath), JournalDir)
	case r.layout == LayoutRepo:
		r.lockPath = filepath.Join(real, LoreDir, LockFileName)
		r.journalDir = filepath.Join(real, LoreDir, JournalDir)
	default:
		// a folder store: one plainly named state folder (D-c)
		r.lockPath = filepath.Join(real, StoreStateDir, LockFileName)
		r.journalDir = filepath.Join(real, StoreStateDir)
	}
	return r, nil
}

// Dir is the store folder (absolute, symlinks resolved).
func (r *Root) Dir() string { return r.dir }

// Layout is where this root's folders keep their rows.
func (r *Root) Layout() Layout { return r.layout }

// LockPath is the lock file's absolute path.
func (r *Root) LockPath() string { return r.lockPath }

// Folder returns one data folder of the store; name is FolderRoot ("." or
// ""), a project folder or FolderOrg. The folder need not exist yet: a
// write creates it.
func (r *Root) Folder(name string) (*Folder, error) {
	clean, err := CleanFolder(name)
	if err != nil {
		return nil, err
	}
	l := r.layout
	if l == LayoutStore && isDir(filepath.Join(r.dir, filepath.FromSlash(LayoutRepo.DataPath(clean)))) {
		// a folder still in the old layout (<folder>/.lore/data: v0.1.17,
		// and stores before the plain-files round) is read and written
		// where its rows are until it is moved (Folder.OldLayout)
		l = LayoutRepo
	}
	rel := l.DataPath(clean)
	return &Folder{root: r, name: clean, layout: l, rel: rel, dataDir: filepath.Join(r.dir, filepath.FromSlash(rel))}, nil
}

// isDir reports whether p is a folder (not a link to one).
func isDir(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.IsDir()
}

// Lock is the held store lock. Release is idempotent.
//
// A nested Lock (taken with a ctx from Hold that already holds this lock
// file) owns nothing: its Release is a no-op and the outer holder releases
// (OV21/OV62 reentrant lock for meta rebuild).
type Lock struct {
	once   sync.Once
	file   *fileLock
	gate   chan struct{}
	err    error
	nested bool
	mu     sync.Mutex
	done   bool // released (read by nested lookups)
}

// Release drops the OS lock and lets the next writer of this process in.
func (l *Lock) Release() error {
	if l.nested {
		return nil
	}
	l.once.Do(func() {
		l.mu.Lock()
		l.done = true
		l.mu.Unlock()
		l.err = l.file.release()
		<-l.gate
	})
	return l.err
}

// held reports whether l is still held.
func (l *Lock) held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.done
}

// heldKey is the context key of a lock held through Hold, per lock file:
// keyed by path, not by *Root, so every Root of one store (the folders of
// one clone open their own) sees the same hold.
type heldKey struct{ path string }

// Hold takes the store lock (as Lock does) and returns ctx carrying it:
// every Lock call made with the returned ctx, on any Root with the same
// lock file, reuses the held lock instead of waiting for it (a reentrant
// lock), so a caller can hold the store across several writes and syncs
// (meta rebuild, OV21/OV62). The caller releases the returned Lock;
// after that the ctx no longer counts as holding.
func (r *Root) Hold(ctx context.Context) (context.Context, *Lock, error) {
	l, err := r.Lock(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if l.nested {
		return ctx, l, nil // already held by ctx: the outer holder releases
	}
	return context.WithValue(ctx, heldKey{path: r.lockPath}, l), l, nil
}

// enterGate takes this process's writer gate, waiting up to the lock
// timeout. A free gate is taken at once, before any timer is looked at: on
// a loaded machine the goroutine can be descheduled past the whole timeout
// before it reaches the wait, and a select over a free gate and an expired
// timer picks either one, so a lock nobody held was refused as "stayed
// held". The same holds when the timer fires while the gate frees up.
func (r *Root) enterGate(ctx context.Context) error {
	select {
	case r.gate <- struct{}{}:
		return nil
	default:
	}
	deadline := time.NewTimer(r.lockWait)
	defer deadline.Stop()
	select {
	case r.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-deadline.C:
		select {
		case r.gate <- struct{}{}:
			return nil
		default:
			return lockedErr(r.lockWait)
		}
	}
}

// Lock takes the exclusive store lock, waiting up to the lock timeout, then
// store.ErrLocked (E_STORE_LOCKED). Reads never need it: every file is
// replaced by rename (spec 6.2 Locking).
func (r *Root) Lock(ctx context.Context) (*Lock, error) {
	if outer, ok := ctx.Value(heldKey{path: r.lockPath}).(*Lock); ok && outer.held() {
		return &Lock{nested: true}, nil // reentrant: ctx already holds it (Hold)
	}
	start := time.Now()
	if err := r.enterGate(ctx); err != nil {
		return nil, err
	}
	f, err := acquireFile(ctx, r.lockPath, r.lockWait-time.Since(start))
	if err != nil {
		<-r.gate
		return nil, err
	}
	return &Lock{file: f, gate: r.gate}, nil
}
