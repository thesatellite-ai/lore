package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
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
	// paths is the file each source's cache was opened at. Two sources
	// over one place (two stores.yaml names with one Location, issue #1)
	// open the same file, which then holds each row once per name; search
	// reads such a file once and counts each row once.
	paths map[store.Source]string
	// combined is the one file shared by every attached store past
	// MaxStoreCaches (CFG3 fallback), nil otherwise.
	combined *sqlite.Cache
	// combinedPath is the file of combined ("" without one): the lens
	// attaches it (lensSources), on a repo's open and a store-only one.
	combinedPath string
	// keys is, per attach name, the `_source` value the name's rows are
	// kept under in its file (keyOf). A store folder's file keys them by
	// the folder's identity, never the name (folderKey).
	keys map[store.Source]store.Source
	// storesRoot is <home>/cache/stores, the folder of the per store
	// folder files ("" in a test router: every name is its own key).
	storesRoot string
	// healed are the notices of damaged cache files moved aside and
	// rebuilt during open (openHealing); the App hands them to
	// Options.Notice once the open succeeds.
	healed []string
}

// Combined reports whether this repo uses the CFG3 combined cache (for
// `lore doctor`: "more than MaxStoreCaches store folders: one combined cache").
func (a *App) Combined() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st != nil && a.st.caches.combined != nil
}

var _ store.Cache = (*caches)(nil)

// newCaches returns an empty router that keys every name's rows by the
// name itself (tests; an open uses newCachesFor).
func newCaches() *caches {
	return &caches{by: map[store.Source]*sqlite.Cache{}, paths: map[store.Source]string{}, keys: map[store.Source]store.Source{}}
}

// newCachesFor returns an empty router for an open over home: the files
// under <home>/cache/stores key their rows by the folder's identity.
func newCachesFor(home string) *caches {
	c := newCaches()
	c.storesRoot = filepath.Join(writableHome(home), cacheDir, cacheStoresDir)
	return c
}

// folderKeyPrefix starts the `_source` value of a store folder's rows; no
// store name starts with it, so a key never reads as a name.
const folderKeyPrefix = "@"

// folderKey is the `_source` value the rows of the store folder cached at
// path are kept under: "@" + the file's place under <home>/cache/stores
// (store id / branch / [layer /] folder, cachePath), the same whatever
// name a checkout attaches the folder under; "" for any other file (the
// repo's own cache, a combined one), whose rows stay under their name.
//
// Why (devco D-933, hunt H-194, public issue #142): one folder's file is
// shared by every checkout and every open that reads the folder, and its
// rows were keyed by the attach name, so two names (`kb/app` in one
// checkout, `notes` in another, or the name before a rename) kept two
// copies of every row. An edit refreshed only the copy of the name that
// read it, and search, which reads the whole file, could show the other
// copy's old text. Keyed by the folder, the file holds one copy of each
// row; an edit replaces it; a rename has nothing left to drop.
func (c *caches) folderKey(path string) store.Source {
	if c.storesRoot == "" {
		return ""
	}
	rel, err := filepath.Rel(c.storesRoot, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}
	return store.Source(folderKeyPrefix + filepath.ToSlash(strings.TrimSuffix(rel, cacheExt)))
}

// keyOf is the `_source` value name's rows are kept under in its file:
// the folder key for a store folder's file, the name itself otherwise.
// Every call into a sqlite.Cache that names a source goes through it.
func (c *caches) keyOf(name store.Source) store.Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[name]; ok {
		return k
	}
	return name
}

// nameOf is the attach name a `_source` value read back from a file
// stands for in this open: the nearest name (repo, then stores.yaml
// order) kept under key, or key itself when no name is (a combined file's
// own names, an older lore's leftover). Every result that carries a
// source from a file goes through it, so a person never sees a key.
func (c *caches) nameOf(key store.Source) store.Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.order {
		if k, ok := c.keys[s]; ok && k == key {
			return s
		}
	}
	return key
}

// at returns name's cache file and the key its rows are kept under there.
func (c *caches) at(name store.Source) (*sqlite.Cache, store.Source, error) {
	f, err := c.of(name)
	if err != nil {
		return nil, "", err
	}
	return f, c.keyOf(name), nil
}

// setKey records the key of name opened at path (caller holds c.mu).
func (c *caches) setKey(name store.Source, path string) {
	if k := c.folderKey(path); k != "" {
		c.keys[name] = k
		return
	}
	c.keys[name] = name
}

// open opens source's cache file at path. A path another source already
// opened shares that source's handle, like the combined cache does: two
// handles on one file each kept their own list of typed tables, so a kind
// table one created or dropped was missing from the other's, and its next
// write failed E_INTERNAL (devco:D-667: a repo linked with `--project org`
// names the org folder as its own source, at the org store's file).
func (c *caches) open(ctx context.Context, source store.Source, path string, reg *schema.Registry, exp sqlite.Expander) error {
	if f := c.openedAt(path); f != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.by[source] = f
		c.paths[source] = path
		c.setKey(source, path)
		c.order = append(c.order, source)
		return nil
	}
	f, err := c.openHealing(ctx, path, reg, exp)
	if err != nil {
		return wrapOpen(err, "wire: open the cache of store "+string(source))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[source] = f
	c.paths[source] = path
	c.setKey(source, path)
	c.order = append(c.order, source)
	return nil
}

// openedAt is the handle a source already opened at path, nil if none.
func (c *caches) openedAt(path string) *sqlite.Cache {
	c.mu.Lock()
	defer c.mu.Unlock()
	for s, p := range c.paths {
		if p == path {
			return c.by[s]
		}
	}
	return nil
}

// openCombined opens ONE cache file at path for every source in names
// (the CFG3 fallback): each source's rows are applied under its own name,
// so the file's `_source` column tells them apart. A combined file is one
// checkout's own (named by its path, or by the names and places of a
// store-only open), so no other name fills it; a rename within the
// checkout is dropped by dropUnusedNames.
func (c *caches) openCombined(ctx context.Context, names []store.Source, path string, reg *schema.Registry, exp sqlite.Expander) error {
	f, err := c.openHealing(ctx, path, reg, exp)
	if err != nil {
		return wrapOpen(err, "wire: open the combined store cache")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range names {
		c.by[n] = f
		c.keys[n] = n
		c.order = append(c.order, n)
	}
	c.combined = f
	c.combinedPath = path
	return nil
}

// wrapOpen wraps a cache open failure under msg, keeping its code and the
// hint of the refusal inside it: the CLI prints the outermost error only,
// so without this an E_SYMLINK_DB refusal would lose the file it names.
func wrapOpen(err error, msg string) error {
	w := errs.Prefix(msg, err)
	var in *errs.Error
	if errors.As(err, &in) && in.Hint != "" {
		return w.WithHint(in.Hint)
	}
	return w
}

// corruptSuffix names a damaged cache file moved aside:
// <file>.corrupt-<UTC timestamp>. It is kept for a week, so a person can
// inspect it, then removed (hometidy.go; `lore doctor` names the copies
// kept: a cache rebuilds from the row files, so nothing is lost).
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
	seeded := seedDryCache(path) // dryhome.go: a dry run starts from a copy of the real cache
	f, err := cacheOpen(ctx, path, reg, exp)
	if err != nil && seeded && sqlite.IsCorrupt(err) {
		dropDryCache(path) // a damaged copy: build the scratch cache from the row files instead
		f, err = cacheOpen(ctx, path, reg, exp)
	}
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
	f, key, err := c.at(source)
	if err != nil {
		return err
	}
	return f.Apply(ctx, key, rows, next)
}

// Cursor implements store.Cache.
func (c *caches) Cursor(ctx context.Context, source store.Source) (store.Cursor, error) {
	f, key, err := c.at(source)
	if err != nil {
		return "", err
	}
	return f.Cursor(ctx, key)
}

// Mirror implements store.MirrorCache (perf3): source's row mirror in the
// file that caches source; nil when that file cannot be opened, and a read
// then opens the row files as before.
func (c *caches) Mirror(source store.Source) store.RowMirror {
	f, key, err := c.at(source)
	if err != nil {
		return nil
	}
	return f.Mirror(key)
}

// Rebuild implements store.Cache. Rows the same file holds under any
// other key (an older lore's copies under an attach name, a combined
// file's rows under a name no attachment uses any more) go too: they were
// searched with the rest and no edit or rebuild reached them (devco
// D-920, D-933, public issue #142).
func (c *caches) Rebuild(ctx context.Context, source store.Source) error {
	f, key, err := c.at(source)
	if err != nil {
		return err
	}
	if err := f.Rebuild(ctx, key); err != nil {
		return err
	}
	for _, old := range c.leftoverSources(ctx, f) {
		if err := f.Rebuild(ctx, old); err != nil {
			return err
		}
	}
	return nil
}

// dropUnusedNames removes from every cache file this open uses the rows
// kept under any key this open does not apply it under (leftoverSources),
// at once on the open, a repo's and a store-only one. Why (devco D-930,
// D-933, public issue #142): an older lore keyed a folder's rows by the
// attach name, so its file can still hold one copy per name it was read
// under; those copies are dropped on the first open (the folder's rows
// are read again under the folder key), and a combined file drops the
// rows of a name the checkout no longer attaches. A file that cannot be
// read is left to the doctor check.
func (c *caches) dropUnusedNames(ctx context.Context) error {
	c.mu.Lock()
	files := map[*sqlite.Cache]bool{}
	var order []*sqlite.Cache
	for _, s := range c.order {
		if f := c.by[s]; f != nil && !files[f] {
			files[f] = true
			order = append(order, f)
		}
	}
	c.mu.Unlock()
	var all []error
	for _, f := range order {
		for _, old := range c.leftoverSources(ctx, f) {
			all = append(all, f.Rebuild(ctx, old))
		}
	}
	return errors.Join(all...)
}

// leftoverSources lists the keys f holds rows under that this open does
// not apply f under. A store folder's file has one key (folderKey), the
// same in every checkout, so anything else there is an older lore's copy
// under an attach name; a combined file belongs to one checkout, so a
// name it no longer attaches is gone for good.
func (c *caches) leftoverSources(ctx context.Context, f *sqlite.Cache) []store.Source {
	c.mu.Lock()
	keep := map[store.Source]bool{}
	for s, g := range c.by {
		if g == f {
			if k, ok := c.keys[s]; ok {
				keep[k] = true
			} else {
				keep[s] = true
			}
		}
	}
	c.mu.Unlock()
	all, err := f.Sources(ctx)
	if err != nil {
		return nil
	}
	var out []store.Source
	for _, s := range all {
		if !keep[s] {
			out = append(out, s)
		}
	}
	return out
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
	c.paths = map[store.Source]string{}
	c.keys = map[store.Source]store.Source{}
	c.order = nil
	c.combined = nil
	c.combinedPath = ""
	return errors.Join(all...)
}

// search runs match over table in every cache, in store order, and keeps
// the first limit hits: each store's hits are best first, and the nearer
// store (repo, then stores.yaml order) wins a tie by coming first.
//
// shadowed are the names a read across stores skips (the later names of a
// place attached twice, Engine.ReadStores): their rows are found through
// the first name, so each row is a hit once and is labelled with that name
// (issue #1). A file that several names were opened at holds each row once
// (folderKey), so it is searched once.
func (c *caches) search(ctx context.Context, table row.Table, match string, limit int, shadowed map[store.Source]bool) ([]opsmemory.Hit, error) {
	return c.searchWith(ctx, table, match, limit, shadowed, false)
}

// searchWith is search; bare leaves every snippet out (sqlite.Cache.
// SearchHitsBare, devco:D-808).
func (c *caches) searchWith(ctx context.Context, table row.Table, match string, limit int, shadowed map[store.Source]bool, bare bool) ([]opsmemory.Hit, error) {
	hits := (*sqlite.Cache).SearchHits
	if bare {
		hits = (*sqlite.Cache).SearchHitsBare
	}
	c.mu.Lock()
	order := append([]store.Source(nil), c.order...)
	combined := c.combined
	paths := make(map[store.Source]string, len(c.paths))
	for s, p := range c.paths {
		paths[s] = p
	}
	c.mu.Unlock()
	var out []opsmemory.Hit
	searched := false
	searchedPath := map[string]bool{}
	for _, s := range order {
		if len(out) >= limit {
			break
		}
		if shadowed[s] {
			continue
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
			got, serr := hits(f, ctx, table, match, limit-len(out))
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
		path := paths[s]
		if searchedPath[path] {
			continue
		}
		searchedPath[path] = true
		// One copy of each row per file (folderKey); an id is still kept
		// once, should an older lore's copy not be dropped yet.
		got, err := hits(f, ctx, table, match, limit-len(out))
		if err != nil {
			return nil, err
		}
		seen := map[ids.AnyID]bool{}
		for _, h := range got {
			if len(out) >= limit {
				break
			}
			if seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			out = append(out, opsmemory.Hit{Source: s, ID: h.ID, Score: h.Score, Snippet: h.Snippet})
		}
	}
	return out, nil
}

// kindNames is every attached cache's kind id -> name (sqlite.Cache.
// KindNames), each file read once; the first cache in order (repo first)
// keeps a shared id.
func (c *caches) kindNames(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	order := append([]store.Source(nil), c.order...)
	c.mu.Unlock()
	out := map[string]string{}
	read := map[*sqlite.Cache]bool{}
	for _, s := range order {
		f, err := c.of(s)
		if err != nil {
			return nil, err
		}
		if read[f] {
			continue
		}
		read[f] = true
		names, err := f.KindNames(ctx)
		if err != nil {
			return nil, err
		}
		for id, name := range names {
			if _, ok := out[id]; !ok {
				out[id] = name
			}
		}
	}
	return out, nil
}
