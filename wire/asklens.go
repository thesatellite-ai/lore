package wire

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/vecfile"
	"github.com/thesatellite-ai/lore/model/errs"
	opsask "github.com/thesatellite-ai/lore/ops/ask"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// AskLens opens one read-only lens over the stores this call reads (the sql
// lens: every folder of a store attached whole, sensitive values masked)
// and runs fn with it, so `lore ask` runs its few statements on one open
// instead of paying the open per statement (devco D-807). repoOnly is
// `--store repo`: the repo's own rows alone (lensScope, devco D-799); any
// other --store is routed before the op runs (RunIn.RoutedStore). The lens
// exposes only the tables ask reads (opsask.LensTables, devco D-813).
func (a *App) AskLens(ctx context.Context, repoOnly bool, fn func(q opsask.Query, keeps bool) error) error {
	return a.withExtra(a.lensFolders(ctx), func() error {
		l, _, err := a.lensFor(ctx, sqlite.LensOptions{MaskSensitive: true, Now: a.Now(), Tables: opsask.LensTables}, lensScope{RepoOnly: repoOnly}, "")
		if err != nil {
			return err
		}
		ferr := fn(func(ctx context.Context, q string) (opssql.Result, error) {
			query := l.QueryTimeout
			if opsask.FileOnly(q) {
				query = l.QueryKept // kept by a pooled lens until a file changes (D-842)
			}
			res, qerr := query(ctx, q, opssql.DefaultTimeout)
			if qerr != nil {
				return opssql.Result{}, qerr
			}
			return opssql.Result{Columns: res.Columns, Rows: res.Rows}, nil
		}, l.Keeps())
		if cerr := l.Close(); ferr == nil && cerr != nil {
			return errs.Wrap(errs.Internal, "wire: close the lens", cerr)
		}
		return ferr
	})
}

// askVectorsFile is the file under the lore home's cache that keeps the
// row vectors of lore ask between processes (adapters/vecfile, devco
// D-902).
const askVectorsFile = "ask-vectors.bin"

// Vectors implements opsask.VectorFile: the vectors earlier calls kept in
// the home's cache, so a one-shot ask embeds only the rows it has not seen.
func (a *App) Vectors(model string, dim int) map[uint64][]float32 {
	return vecfile.Read(filepath.Join(a.opts.Home, cacheDir, askVectorsFile), model, dim)
}

// Keep implements opsask.VectorFile: it adds vecs to the home's vector
// file. Best effort: a file that cannot be written only costs the next
// call the embedding, so the error is dropped.
func (a *App) Keep(model string, dim int, vecs map[uint64][]float32) {
	_ = vecfile.Write(filepath.Join(a.opts.Home, cacheDir, askVectorsFile), model, dim, vecs)
}

// App keeps lore ask's vectors in the home's cache (opsask.VectorFile).
var _ opsask.VectorFile = (*App)(nil)

// AskHome implements opsask.HomeEnv: the <org>/<folder> this repo's org
// link names (linkedFolderTarget, what lore whoami prints as the reads of
// "this repo's link"), the place its rows carry; "" outside a code repo,
// in a store checkout and in a repo linked to no folder (devco D-963).
// Why not LinkedFolderStore: that one names only a repo with lore files
// of its own, and an own repo linked by --own has none.
func (a *App) AskHome(ctx context.Context) string {
	if a.opts.RepoDir == "" || inStore(a.opts.RepoDir) {
		return ""
	}
	// an own repo linked by --own has no lore files, so storeRepoTop
	// errs; top only renames a store name the repo's stores.yaml also
	// uses, and "" renames none
	top, _ := a.storeRepoTop(ctx)
	return a.linkedFolderTarget(ctx, top)
}

// App names the repo's linked folder to lore ask (opsask.HomeEnv).
var _ opsask.HomeEnv = (*App)(nil)
