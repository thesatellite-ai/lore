package wire

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/store"
)

// Lens opens the read-only cross-store lens of spec 7.4 over this repo's
// caches, for one command (`lore sql`): every attached store's cache file
// with the store's name as the `source` column, or, past MaxStoreCaches,
// the repo's own file plus the one combined file whose rows carry their own
// source (CFG3, O10). The caller closes it.
//
// Client repos (MQ6, CFG6): when the repo store is client-class, the lens
// attaches ONLY the repo's own cache, never a store's data. The engine's
// answer already fails closed (no RepoIsClient hook, or the check failed,
// counts as client: see repoIsClient), so an unknown answer also gets the
// repo-only lens.
//
// Invariant: the lens reads the files the engine refreshed at open, through
// its own read-only connection (mode=ro, query_only); nothing it runs can
// change a cache.
func (a *App) Lens(ctx context.Context, o sqlite.LensOptions) (*sqlite.Lens, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	srcs, err := a.lensSources(st)
	if err != nil {
		return nil, err
	}
	return sqlite.OpenLensWith(ctx, srcs, o)
}

// lensSources lists the cache files of st's lens, the repo first (the
// nearer store wins an unqualified name, as everywhere else).
func (a *App) lensSources(st *state) ([]sqlite.LensSource, error) {
	repo := sqlite.LensSource{
		Name: string(store.SourceRepo),
		Path: filepath.Join(st.repoTop, filepath.FromSlash(RepoCacheFile)),
	}
	srcs := []sqlite.LensSource{repo}
	if st.eng.IsClient(store.SourceRepo) {
		return srcs, nil
	}
	st.caches.mu.Lock()
	combined := st.caches.combined != nil
	st.caches.mu.Unlock()
	if combined {
		path, err := combinedCachePath(a.opts.Home, st.repoTop)
		if err != nil {
			return nil, err
		}
		return append(srcs, sqlite.LensSource{Name: combinedLensName, Path: path, Combined: true}), nil
	}
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		path, err := cachePath(a.opts.Home, attach.Opened{
			Attached: engine.Attached{Name: name, Backend: st.byName[name]},
			Folder:   st.folders[name],
		})
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, sqlite.LensSource{Name: string(name), Path: path})
	}
	return srcs, nil
}

// combinedLensName labels the combined cache file in a lens; it never
// reaches a result, since a combined source's rows carry their own store
// name in _source.
const combinedLensName = "combined"

// SQL implements ops/sql.Env: it opens a lens for this one query (masked
// unless showSensitive), runs it and closes the lens.
func (a *App) SQL(ctx context.Context, query string, showSensitive bool) (opssql.Result, error) {
	l, err := a.Lens(ctx, sqlite.LensOptions{MaskSensitive: !showSensitive})
	if err != nil {
		return opssql.Result{}, err
	}
	res, qerr := l.Query(ctx, query)
	if cerr := l.Close(); qerr == nil && cerr != nil {
		return opssql.Result{}, errs.Wrap(errs.Internal, "wire: close the lens", cerr)
	}
	if qerr != nil {
		return opssql.Result{}, qerr
	}
	return opssql.Result{Columns: res.Columns, Rows: res.Rows}, nil
}
