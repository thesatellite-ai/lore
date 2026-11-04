package wire

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// On-disk names wire chooses (spec 7.4 cache layout).
const (
	// RepoCacheFile is the repo store's cache, relative to the repo top.
	// It is not .lore/lore.db: the v0.1.17 code still owns that file.
	RepoCacheFile = ".lore/lore-cache.db"
	// cacheDir holds the other stores' caches under the lore home.
	cacheDir = "cache"
	// cacheStoresDir is the folder of per-store caches under cacheDir.
	cacheStoresDir = "stores"
	// cacheExt ends a cache file name.
	cacheExt = ".db"
	// rootFolderName names the cache file of a store's root folder (".").
	rootFolderName = "root"
	// localBranch is the branch segment of a store with no branches (a
	// folder store).
	localBranch = "local"
	// cacheDirMode is the mode of cache folders lore creates (owner only:
	// caches hold internal rows).
	cacheDirMode = 0o700
)

// open builds the whole core for opts (see the package doc for the order).
// On any failure everything opened so far is closed.
func open(ctx context.Context, opts Options) (*state, error) {
	actor := opts.Actor
	if actor.IsZero() {
		a, err := opts.ResolveActor(ctx)
		if err != nil {
			return nil, err
		}
		actor = a
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	st := &state{actor: actor}
	fail := func(err error) (*state, error) { return nil, errors.Join(err, st.close()) }

	repo, err := git.OpenRepo(ctx, opts.RepoDir, rowfs.FolderRoot, store.OpenOptions{Home: opts.Home})
	if err != nil {
		return nil, err
	}
	st.backends = append(st.backends, repo)
	st.repoTop = repo.Dir()

	attached, err := openAttached(ctx, st.repoTop, opts.Home, reg)
	if err != nil {
		return fail(err)
	}
	for _, o := range attached {
		st.backends = append(st.backends, o.Backend)
	}

	st.caches = newCaches()
	exp := refs.Expander{} // no resolver yet: vars/resources are not migrated, references stay as written
	repoCache := filepath.Join(st.repoTop, filepath.FromSlash(RepoCacheFile))
	if mkErr := os.MkdirAll(filepath.Dir(repoCache), cacheDirMode); mkErr != nil {
		return fail(errs.Wrap(errs.Internal, "wire: create the .lore folder", mkErr))
	}
	if err = st.caches.open(ctx, store.SourceRepo, repoCache, reg, exp); err != nil {
		return fail(err)
	}
	for _, o := range attached {
		path, pathErr := cachePath(opts.Home, o)
		if pathErr != nil {
			return fail(pathErr)
		}
		if err = st.caches.open(ctx, o.Name, path, reg, exp); err != nil {
			return fail(err)
		}
	}

	backends := map[store.Source]store.Backend{store.SourceRepo: repo}
	for _, o := range attached {
		backends[o.Name] = o.Backend
	}
	scanner, err := newScanner(ctx, backends)
	if err != nil {
		return fail(err)
	}
	st.audit = &fileAudit{path: filepath.Join(st.repoTop, filepath.FromSlash(AuditFile))}

	engOpts := []engine.Option{
		engine.WithStore(engine.Attached{Name: store.SourceRepo, Backend: repo}),
		engine.WithCache(st.caches), engine.WithRegistry(reg), engine.WithScanner(scanner),
		engine.WithActor(actor), engine.WithAudit(st.audit), engine.WithWall(opts.Wall),
	}
	for _, o := range attached {
		engOpts = append(engOpts, engine.WithStore(o.Attached))
	}
	if st.eng, err = engine.New(engOpts...); err != nil {
		return fail(err)
	}
	for _, name := range st.eng.Stores() {
		if _, refErr := st.eng.Refresh(ctx, name); refErr != nil {
			return fail(errs.Wrap(errs.CodeOf(refErr), "wire: refresh the cache of store "+string(name), refErr))
		}
	}
	if st.format, err = readFormat(ctx, repo, actor); err != nil {
		return fail(err)
	}
	return st, nil
}

// openAttached reads <repo>/.lore/stores.yaml (absent = no stores) and
// opens every store it lists.
func openAttached(ctx context.Context, repoTop, home string, reg *schema.Registry) ([]attach.Opened, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(repoTop, filepath.FromSlash(attach.FileName))))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: read "+attach.FileName, err)
	}
	cfg, err := attach.Parse(b)
	if err != nil {
		return nil, err
	}
	for i, s := range cfg.Stores {
		if cfg.Stores[i].URL, err = canonical(s); err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "wire: store "+string(s.Name), err)
		}
	}
	openers, err := newOpeners(reg)
	if err != nil {
		return nil, err
	}
	return attach.Open(ctx, cfg, openers, store.OpenOptions{Home: home})
}

// canonical prepares one stores.yaml url for store.Openers: the scp form
// (git@host:owner/repo) becomes ssh:// through git.CanonicalURL (net/url
// cannot parse it), a password is refused (attach.NormalizeURL, never
// echoed), and the entry's folder becomes the `?folder=` the backends read
// (rowfs.FolderParam).
func canonical(s attach.Store) (string, error) {
	raw, err := attach.NormalizeURL(s.URL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	if u.Scheme != folder.Scheme {
		if raw, err = git.CanonicalURL(raw); err != nil {
			return "", err
		}
		if u, err = url.Parse(raw); err != nil {
			return "", errs.New(errs.StoreScheme, "wire: the store url does not parse")
		}
	}
	if s.Folder != "" {
		q := u.Query()
		q.Set(rowfs.FolderParam, s.Folder)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// newOpeners registers the 0.1.18 backends: folder for file://, git for
// ssh, https and http with logic/merge as the row merger (spec 11 rule 5:
// the same merge the engine runs).
func newOpeners(reg *schema.Registry) (*store.Openers, error) {
	o := store.NewOpeners()
	if err := o.Register(folder.Scheme, folder.NewOpener()); err != nil {
		return nil, err
	}
	gitOpen := git.NewOpener(git.WithMerger(merge.Driver{Registry: reg}))
	for _, s := range git.Schemes {
		if err := o.Register(s, gitOpen); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// markerOf is implemented by both 0.1.18 backends: the store's
// lore-store.yaml.
type markerOf interface{ Marker() rowfs.Marker }

// branchOf is implemented by the git backend.
type branchOf interface{ Branch() string }

// cachePath is <home>/cache/stores/<store id>/<branch>/<folder>.db. A
// v0.1.17 marker has no store id; the store's name stands in, so the path
// is still stable for this repo.
func cachePath(home string, o attach.Opened) (string, error) {
	id := string(o.Name)
	if m, ok := o.Backend.(markerOf); ok && !m.Marker().ID.IsZero() {
		id = m.Marker().ID.String()
	}
	branch := localBranch
	if b, ok := o.Backend.(branchOf); ok && b.Branch() != "" {
		branch = b.Branch()
	}
	name := rootFolderName
	if o.Folder != "" && o.Folder != rowfs.FolderRoot {
		clean, err := rowfs.CleanFolder(o.Folder)
		if err != nil {
			return "", err
		}
		name = clean
	}
	dir := filepath.Join(home, cacheDir, cacheStoresDir, id, filepath.FromSlash(branch))
	path := filepath.Join(dir, filepath.FromSlash(name)+cacheExt)
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return "", errs.Wrap(errs.Internal, "wire: create the cache folder", err)
	}
	return path, nil
}

// cacheOpen is the sqlite cache constructor with the expander it needs.
func cacheOpen(ctx context.Context, path string, reg *schema.Registry, exp sqlite.Expander) (*sqlite.Cache, error) {
	return sqlite.Open(ctx, path, reg, exp)
}
