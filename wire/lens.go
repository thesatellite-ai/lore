package wire

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/refs"
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
// Client repos (MQ6, CFG4, CFG6): when the repo store is client-class, the
// lens attaches the repo's own cache plus the stores marked visibility:
// shareable; internal stores join only with internal (`lore sql
// --internal`). The engine's answer already fails closed (no RepoIsClient
// hook, or the check failed, counts as client: see repoIsClient), so an
// unknown answer gets the client lens. Past MaxStoreCaches a client lens
// without --internal stays repo-only (the combined file mixes every store,
// FA-META-1).
//
// Invariant: the lens reads the files the engine refreshed at open, through
// its own read-only connection (mode=ro, query_only); nothing it runs can
// change a cache.
func (a *App) Lens(ctx context.Context, o sqlite.LensOptions) (*sqlite.Lens, error) {
	l, _, err := a.lensFor(ctx, o, false)
	return l, err
}

// lensFor is Lens with the client repo's --internal choice; it also
// returns the internal stores attached only because of internal.
func (a *App) lensFor(ctx context.Context, o sqlite.LensOptions, internal bool) (*sqlite.Lens, []string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, nil, err
	}
	srcs, extra, err := a.lensSources(ctx, st, internal)
	if err != nil {
		return nil, nil, err
	}
	o.Trash = trashIn(o.Trash, srcs, st.eng.Stores())
	if o.Expand == nil {
		// lore_expand(text): var/res/row filled from the row files, never env,
		// secrets or sensitive values (SQ4, SQ15); client repos see only their own (MQ6).
		o.Expand = refs.Expander{Resolver: newResolver(st)}
	}
	l, err := sqlite.OpenLensWith(ctx, srcs, o)
	return l, extra, err
}

// lensSources lists the cache files of st's lens, the repo first (the
// nearer store wins an unqualified name, as everywhere else), and the
// internal stores a client repo attached only because of internal:
//
//	own repo ─▶ every store (or the combined file)
//	client   ─▶ repo + shareable stores; + internal stores with internal (CFG4)
//	client, combined ─▶ repo only; + the combined file with internal
func (a *App) lensSources(ctx context.Context, st *state, internal bool) ([]sqlite.LensSource, []string, error) {
	repo := sqlite.LensSource{
		Name: string(store.SourceRepo),
		Path: filepath.Join(st.repoTop, filepath.FromSlash(RepoCacheFile)),
	}
	srcs := []sqlite.LensSource{repo}
	client := st.eng.IsClient(store.SourceRepo)
	shareable := map[store.Source]bool{}
	if client {
		sh, err := a.ShareableStores(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, n := range sh {
			shareable[n] = true
		}
	}
	var extra []string
	st.caches.mu.Lock()
	combined := st.caches.combined != nil
	st.caches.mu.Unlock()
	if combined {
		if client && !internal {
			return srcs, nil, nil // the combined file mixes internal stores in (fail closed)
		}
		path, err := combinedCachePath(a.opts.Home, st.repoTop)
		if err != nil {
			return nil, nil, err
		}
		if client {
			for _, name := range st.eng.Stores() {
				if name != store.SourceRepo && !shareable[name] {
					extra = append(extra, string(name))
				}
			}
		}
		return append(srcs, sqlite.LensSource{Name: combinedLensName, Path: path, Combined: true}), extra, nil
	}
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		if client && !shareable[name] {
			if !internal {
				continue // CFG4: an internal store stays out of a client repo's lens
			}
			extra = append(extra, string(name))
		}
		path, err := cachePath(a.opts.Home, attach.Opened{
			Attached: engine.Attached{Name: name, Backend: st.byName[name]},
			Folder:   st.folders[name],
		})
		if err != nil {
			return nil, nil, err
		}
		srcs = append(srcs, sqlite.LensSource{Name: string(name), Path: path})
	}
	return srcs, extra, nil
}

// trashIn keeps the trash rows of the stores the lens attached, so a client
// repo's `trash` view never lists an internal store's titles (CFG4). A
// combined source stands for every attached store.
func trashIn(rows []sqlite.TrashRow, srcs []sqlite.LensSource, all []store.Source) []sqlite.TrashRow {
	keep := map[string]bool{}
	for _, s := range srcs {
		if s.Combined {
			for _, n := range all {
				keep[string(n)] = true
			}
			continue
		}
		keep[s.Name] = true
	}
	out := rows[:0:0]
	for _, r := range rows {
		if keep[r.Source] {
			out = append(out, r)
		}
	}
	return out
}

// combinedLensName labels the combined cache file in a lens; it never
// reaches a result, since a combined source's rows carry their own store
// name in _source.
const combinedLensName = "combined"

// SQL implements ops/sql.Env: it opens a lens for this one query (masked
// unless r.ShowSensitive), runs it under r.Timeout (E_SQL_TIMEOUT past it)
// and closes the lens.
func (a *App) SQL(ctx context.Context, r opssql.Request) (opssql.Result, error) {
	trashRows, err := a.lensTrash(ctx, !r.ShowSensitive) // DELETION 3.2: the cache keeps no trashed row
	if err != nil {
		return opssql.Result{}, err
	}
	l, extra, err := a.lensFor(ctx, sqlite.LensOptions{MaskSensitive: !r.ShowSensitive, Trash: trashRows}, r.Internal)
	if err != nil {
		return opssql.Result{}, err
	}
	res, qerr := l.QueryTimeout(ctx, r.Query, r.Timeout)
	if cerr := l.Close(); qerr == nil && cerr != nil {
		return opssql.Result{}, errs.Wrap(errs.Internal, "wire: close the lens", cerr)
	}
	if qerr != nil {
		return opssql.Result{}, qerr
	}
	return opssql.Result{Columns: res.Columns, Rows: res.Rows, Internal: extra}, nil
}
