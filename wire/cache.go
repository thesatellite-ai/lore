package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// caches is the engine's store.Cache: one SQLite cache file per attached
// store (spec 7.4, CFG3 refined: a cache is per store folder, shared by
// every repo using it, so deleting one store's cache never touches
// another's), routed by source.
//
// Why a router and not one file: the engine's cache port names the source
// on every call, and per-store files let `lore` drop or rebuild one store's
// copy alone; search then asks each file in store precedence order.
type caches struct {
	mu    sync.Mutex
	order []store.Source
	by    map[store.Source]*sqlite.Cache
	// combined is the one file shared by every attached store past
	// MaxStoreCaches (CFG3 fallback), nil otherwise.
	combined *sqlite.Cache
	// healed are the notices of damaged cache files moved aside and
	// rebuilt during open (openHealing); the App hands them to
	// Options.Notice once the open succeeds.
	healed []string
}

// Combined reports whether this repo uses the CFG3 combined cache (for
// `lore doctor`: "more than 9 store folders: one combined cache").
func (a *App) Combined() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st != nil && a.st.caches.combined != nil
}

var _ store.Cache = (*caches)(nil)

// newCaches returns an empty router.
func newCaches() *caches { return &caches{by: map[store.Source]*sqlite.Cache{}} }

// open opens source's cache file at path.
func (c *caches) open(ctx context.Context, source store.Source, path string, reg *schema.Registry, exp sqlite.Expander) error {
	f, err := c.openHealing(ctx, path, reg, exp)
	if err != nil {
		return errs.Wrap(errs.CodeOf(err), "wire: open the cache of store "+string(source), err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[source] = f
	c.order = append(c.order, source)
	return nil
}

// openCombined opens ONE cache file at path for every source in names
// (the CFG3 fallback): each source's rows are applied under its own name,
// so the file's `_source` column tells them apart.
func (c *caches) openCombined(ctx context.Context, names []store.Source, path string, reg *schema.Registry, exp sqlite.Expander) error {
	f, err := c.openHealing(ctx, path, reg, exp)
	if err != nil {
		return errs.Wrap(errs.CodeOf(err), "wire: open the combined store cache", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range names {
		c.by[n] = f
		c.order = append(c.order, n)
	}
	c.combined = f
	return nil
}

// corruptSuffix names a damaged cache file moved aside:
// <file>.corrupt-<UTC timestamp>. It is kept, never deleted, so nothing a
// person might want to inspect is lost silently.
const corruptSuffix = ".corrupt-"

// corruptStamp is the timestamp layout of a moved-aside file (sortable,
// no characters a file system refuses).
const corruptStamp = "20060102T150405Z"

// cacheSidecars are the files SQLite keeps beside a cache in WAL mode;
// they belong to the damaged file and move aside with it (a stale WAL
// replayed onto a fresh file would corrupt it again).
var cacheSidecars = []string{"-wal", "-shm"}

// healCaches turns self-healing of damaged cache files on. It is a
// variable only so a test can turn it off and watch the command fail
// (break-verification); nothing else sets it.
var healCaches = true

// openHealing opens the cache file at path. When the open fails because
// the FILE is damaged (sqlite.IsCorrupt; never a lock or permission
// error), the file and its WAL sidecars are renamed to
// <name>.corrupt-<time> and a fresh, empty cache is opened in its place:
// a cache is rebuildable by design (spec 3), and the engine's refresh
// refills it from the row files. One notice per healed file is kept in
// c.healed for the person.
func (c *caches) openHealing(ctx context.Context, path string, reg *schema.Registry, exp sqlite.Expander) (*sqlite.Cache, error) {
	f, err := cacheOpen(ctx, path, reg, exp)
	if err == nil || !healCaches || !sqlite.IsCorrupt(err) {
		return f, err
	}
	base := asideBase(path)
	aside := base + corruptSuffix + time.Now().UTC().Format(corruptStamp)
	for n := 1; fileExists(aside); n++ { // never overwrite an earlier one
		aside = base + corruptSuffix + time.Now().UTC().Format(corruptStamp) + "-" + strconv.Itoa(n)
	}
	if mvErr := os.Rename(path, aside); mvErr != nil {
		return nil, errors.Join(err, errs.Wrap(errs.Internal, "wire: move the damaged cache aside", mvErr))
	}
	for _, s := range cacheSidecars {
		if mvErr := os.Rename(path+s, aside+s); mvErr != nil && !os.IsNotExist(mvErr) {
			return nil, errors.Join(err, errs.Wrap(errs.Internal, "wire: move the damaged cache aside", mvErr))
		}
	}
	f, err = cacheOpen(ctx, path, reg, exp)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.healed = append(c.healed, "lore: the cache "+path+" was damaged; moved it to "+aside+" and rebuilt it from the row files (nothing lost)")
	c.mu.Unlock()
	return f, nil
}

// repoBackupsDir is the folder under .lore that every repo's .gitignore
// already ignores (lore init writes ".lore/backups/").
const repoBackupsDir = "backups"

// repoMarkerDir is the folder the repo cache lives in (wire.RepoCacheFile).
const repoMarkerDir = ".lore"

// asideBase is where a damaged cache at path is moved: the repo cache
// (.lore/lore-cache.db) goes to .lore/backups/, so the kept copy never
// shows in git status; a cache under the lore home stays beside itself
// (that folder is outside every repo).
func asideBase(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) != repoMarkerDir {
		return path
	}
	// The backups folder is made inside .lore through os.Root, so a
	// swapped-in symlink at .lore/backups cannot place it elsewhere.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return path
	}
	if err := errors.Join(root.MkdirAll(repoBackupsDir, cacheDirMode), root.Close()); err != nil {
		return path
	}
	return filepath.Join(dir, repoBackupsDir, filepath.Base(path))
}

// fileExists reports whether p names anything on disk.
func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// of returns source's cache file.
func (c *caches) of(source store.Source) (*sqlite.Cache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.by[source]
	if !ok {
		return nil, errs.New(errs.NotFound, "wire: no cache for store "+string(source))
	}
	return f, nil
}

// Apply implements store.Cache.
func (c *caches) Apply(ctx context.Context, source store.Source, rows []store.Row, next store.Cursor) error {
	f, err := c.of(source)
	if err != nil {
		return err
	}
	return f.Apply(ctx, source, rows, next)
}

// Cursor implements store.Cache.
func (c *caches) Cursor(ctx context.Context, source store.Source) (store.Cursor, error) {
	f, err := c.of(source)
	if err != nil {
		return "", err
	}
	return f.Cursor(ctx, source)
}

// Rebuild implements store.Cache.
func (c *caches) Rebuild(ctx context.Context, source store.Source) error {
	f, err := c.of(source)
	if err != nil {
		return err
	}
	return f.Rebuild(ctx, source)
}

// Close implements store.Cache: every file is closed, every error kept.
func (c *caches) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []error
	closed := map[*sqlite.Cache]bool{}
	for _, s := range c.order {
		if f := c.by[s]; !closed[f] {
			closed[f] = true
			all = append(all, f.Close())
		}
	}
	c.by = map[store.Source]*sqlite.Cache{}
	c.order = nil
	c.combined = nil
	return errors.Join(all...)
}

// search runs match over table in every cache, in store order, and keeps
// the first limit hits: each store's hits are best first, and the nearer
// store (repo, then stores.yaml order) wins a tie by coming first.
func (c *caches) search(ctx context.Context, table row.Table, match string, limit int) ([]opsmemory.Hit, error) {
	c.mu.Lock()
	order := append([]store.Source(nil), c.order...)
	combined := c.combined
	c.mu.Unlock()
	var out []opsmemory.Hit
	searched := false
	for _, s := range order {
		if len(out) >= limit {
			break
		}
		f, err := c.of(s)
		if err != nil {
			return nil, err
		}
		if f == combined {
			if searched {
				continue
			}
			searched = true
			got, serr := f.SearchHits(ctx, table, match, limit-len(out))
			if serr != nil {
				return nil, serr
			}
			for _, h := range got {
				hit, rerr := f.ResolveID(ctx, h.ID)
				if rerr != nil {
					return nil, rerr
				}
				out = append(out, opsmemory.Hit{Source: store.Source(hit.Source), ID: h.ID, Score: h.Score, Snippet: h.Snippet})
			}
			continue
		}
		got, err := f.SearchHits(ctx, table, match, limit-len(out))
		if err != nil {
			return nil, err
		}
		for _, h := range got {
			out = append(out, opsmemory.Hit{Source: s, ID: h.ID, Score: h.Score, Snippet: h.Snippet})
		}
	}
	return out, nil
}
