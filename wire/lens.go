package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/tmpdir"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
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
	l, _, err := a.lensFor(ctx, o, false, "")
	return l, err
}

// lensFor is Lens with the client repo's --internal choice and, when
// deleted is not "", the deleted-rows file (FA-META-2) attached last; it
// also returns the internal stores attached only because of internal.
func (a *App) lensFor(ctx context.Context, o sqlite.LensOptions, internal bool, deleted string) (*sqlite.Lens, []string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, nil, err
	}
	srcs, extra, err := a.lensSources(ctx, st, internal)
	if err != nil {
		return nil, nil, err
	}
	o.Trash = trashIn(o.Trash, srcs, st.eng.Stores())
	if deleted != "" {
		srcs = append(srcs, sqlite.LensSource{Name: deletedLensName, Path: deleted, Deleted: true})
	}
	if o.Places == nil {
		o.Places = a.lensPlaces(st)
	}
	if o.Expand == nil {
		// lore_expand(text): var/res/row filled from the row files, never env,
		// secrets or sensitive values (SQ4, SQ15); client repos see only their own (MQ6).
		o.Expand = refs.Expander{Resolver: newResolver(st)}
	}
	l, err := sqlite.OpenLensWith(ctx, srcs, o)
	return l, extra, err
}

// lensPlaces labels every attached store as search and the lists do
// (devco D-568): the `place` column of the `<table>_all` views, so `lore
// sql` shows "acme/web" where search prints "[acme/web]"; since D-685 the
// `source` column carries the same label. Every store of the engine is
// labelled, not only the lens's own sources, because a combined or
// deleted-rows file holds rows of any of them under their own names; the
// repo's own rows keep their source name (search gives them no label).
func (a *App) lensPlaces(st *state) map[string]string {
	out := map[string]string{}
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		ref, _ := a.sourcePlace(name)
		out[string(name)] = strings.Trim(opssearch.StoreLabel(ref), "[]")
	}
	return out
}

// placeLabels is names as the lens's source column prints them (the
// lensPlaces labels, devco D-685), so the internal-stores note names the
// same sources the rows carry.
func (a *App) placeLabels(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		ref, _ := a.sourcePlace(store.Source(n))
		if store.Source(n) == store.SourceRepo {
			out = append(out, n)
			continue
		}
		out = append(out, strings.Trim(opssearch.StoreLabel(ref), "[]"))
	}
	return out
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
		Path: st.repoCacheOf(),
	}
	srcs := []sqlite.LensSource{repo}
	if repo.Path == "" {
		// a store-only open (a store clone, a read outside any repo) has
		// no repo cache: its stores are the sources (devco D-454; it
		// refused "a source needs a name and a path")
		srcs = nil
	}
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
	path := st.caches.combinedPath // the file the open applied every store into
	st.caches.mu.Unlock()
	if path != "" {
		if client && !internal {
			return srcs, nil, nil // the combined file mixes internal stores in (fail closed)
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
	// a folder attached for this command (lensfolders.go, idfolder.go) is
	// read through a cache file other opens fill under their own names, as
	// is every store's: each keeps only its own name's rows (D-443, D-473)
	markShared(srcs)
	return onePerPlace(srcs), extra, nil
}

// onePerPlace keeps the first source of each cache file (devco D-443,
// owner 2026-11-16, replacing the 2026-11-08 "both names" decision): two
// names on one place (two stores.yaml entries, an org store and a link
// over the same folder) made every `<table>_all` query see each row once
// per name (78 rows for 39 rules). The kept source stays Shared, so its
// views read only the rows applied under its own name.
func onePerPlace(srcs []sqlite.LensSource) []sqlite.LensSource {
	seen := map[string]bool{}
	out := srcs[:0:0]
	for _, s := range srcs {
		if !s.Combined && !s.Deleted {
			if seen[s.Path] {
				continue
			}
			seen[s.Path] = true
		}
		out = append(out, s)
	}
	return out
}

// markShared sets Shared on every store's source (never the repo's own
// cache, which no other open fills): a store's cache file is one per place
// on the machine, and every open that reads the place applies its rows
// under the name it gives it (a repo's `acme/org`, `--store acme`'s
// `acme`, a store clone's `<org>/<folder>`), each once in _source. A
// source's part of the `<table>_all` views keeps only the rows applied
// under its own name, so a query sees each row once whichever opens
// filled the file before (devco D-473: `sql --store acme` after reads
// from a repo and a clone listed every rule three times). Two names on
// one file in this lens are dropped to one by onePerPlace.
func markShared(srcs []sqlite.LensSource) {
	for i := range srcs {
		srcs[i].Shared = srcs[i].Name != string(store.SourceRepo)
	}
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

// deletedLensName labels the deleted-rows file in a lens; like the
// combined file its rows carry their own store name in _source.
const deletedLensName = "deleted"

// deletedCache writes the rows in the trash of the stores the lens will
// attach (the client rule of lensSources, CFG4) into a fresh cache file in
// a temp folder, each under its store's name, for `lore sql
// --include-deleted` (DELETION 2.9, FA-META-2: the store caches hold
// trashed rows only as hidden tombstones). The caller runs cleanup after
// the lens closes; it removes the folder.
//
//	engine.Trashed ─▶ keep attached stores ─▶ sqlite.Open(temp) ─▶ Apply per store ─▶ Close
func (a *App) deletedCache(ctx context.Context, internal bool) (string, func(), error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", nil, err
	}
	srcs, _, err := a.lensSources(ctx, st, internal)
	if err != nil {
		return "", nil, err
	}
	all, err := st.eng.Trashed(ctx)
	if err != nil {
		return "", nil, err
	}
	keep := map[store.Source]bool{}
	for _, s := range srcs {
		if s.Combined {
			for _, n := range st.eng.Stores() {
				keep[n] = true
			}
			continue
		}
		keep[store.Source(s.Name)] = true
	}
	byStore := map[store.Source][]store.Row{}
	var order []store.Source
	for _, t := range all {
		if !keep[t.Store] {
			continue
		}
		sr, rerr := store.FromRow(t.Row)
		if rerr != nil {
			return "", nil, rerr
		}
		if _, seen := byStore[t.Store]; !seen {
			order = append(order, t.Store)
		}
		byStore[t.Store] = append(byStore[t.Store], sr)
	}
	// lore's own tmp folder is the fallback when TMPDIR cannot be used.
	dir, err := tmpdir.Dir("lore-deleted-", filepath.Join(a.LoreHome(), probeTmpDir), a.LoreHome())
	if err != nil {
		return "", nil, errs.Wrap(errs.Internal, "wire: create the deleted-rows folder", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "deleted"+cacheExt)
	c, err := cacheOpen(ctx, path, st.eng.Registry(), refs.Expander{Resolver: newResolver(st)})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	for _, n := range order {
		if aerr := c.Apply(ctx, n, byStore[n], ""); aerr != nil {
			cleanup()
			return "", nil, errors.Join(aerr, c.Close())
		}
	}
	if cerr := c.Close(); cerr != nil {
		cleanup()
		return "", nil, cerr
	}
	return path, cleanup, nil
}

// combinedLensName labels the combined cache file in a lens; it never
// reaches a result, since a combined source's rows carry their own store
// name in _source.
const combinedLensName = "combined"

// SQL implements ops/sql.Env: it opens a lens for this one query (masked
// unless r.ShowSensitive), runs it under r.Timeout (E_SQL_TIMEOUT past it)
// and closes the lens.
func (a *App) SQL(ctx context.Context, r opssql.Request) (opssql.Result, error) {
	// D-443: every folder of a store attached whole joins this one query
	var out opssql.Result
	err := a.withExtra(a.lensFolders(ctx), func() error {
		var qerr error
		out, qerr = a.sqlOnce(ctx, r)
		return qerr
	})
	return out, err
}

// sqlOnce is SQL over the stores attached now.
func (a *App) sqlOnce(ctx context.Context, r opssql.Request) (opssql.Result, error) {
	// DELETION 3.2: the cache keeps no trashed row, so the trash table is
	// filled from the row files, which reads every row of every store:
	// only for a query that can name it. `select 1` on a store with a
	// 5,000-row folder paid that read on every call (devco D-790).
	var trashRows []sqlite.TrashRow
	if queryNamesTrash(r.Query) {
		var err error
		if trashRows, err = a.lensTrash(ctx, !r.ShowSensitive); err != nil {
			return opssql.Result{}, err
		}
	}
	var deleted string
	if r.IncludeDeleted {
		dir, cleanup, derr := a.deletedCache(ctx, r.Internal)
		if derr != nil {
			return opssql.Result{}, derr
		}
		defer cleanup()
		deleted = dir
	}
	// TQ8 / FB-TASK-4: the time_spent view's client column follows the
	// repo's effective time.client_key.
	pol, err := a.Policy(ctx, store.SourceRepo)
	if err != nil {
		return opssql.Result{}, err
	}
	clientKey, _ := pol.Text(policy.KeyTimeClientKey) // unset = the lens default
	l, extra, err := a.lensFor(ctx, sqlite.LensOptions{MaskSensitive: !r.ShowSensitive, Trash: trashRows, TimeClientKey: clientKey, Now: a.Now()}, r.Internal, deleted)
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
	return opssql.Result{Columns: res.Columns, Rows: res.Rows, Internal: a.placeLabels(extra)}, nil
}

// queryNamesTrash reports whether the SQL text q can read the trash table
// (sqlite.ViewTrash): every way to name a table (bare, "quoted", `ticked`,
// [bracketed], any case) has the name in its text, and a lens query cannot
// build a table name at run time. A query that does not name it reads an
// empty trash table, which it never looks at.
func queryNamesTrash(q string) bool {
	return strings.Contains(strings.ToLower(q), sqlite.ViewTrash)
}
