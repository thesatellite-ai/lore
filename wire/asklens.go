package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
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
func (a *App) AskLens(ctx context.Context, repoOnly bool, fn func(q opsask.Query) error) error {
	return a.withExtra(a.lensFolders(ctx), func() error {
		l, _, err := a.lensFor(ctx, sqlite.LensOptions{MaskSensitive: true, Now: a.Now(), Tables: opsask.LensTables}, lensScope{RepoOnly: repoOnly}, "")
		if err != nil {
			return err
		}
		ferr := fn(func(ctx context.Context, q string) (opssql.Result, error) {
			res, qerr := l.QueryTimeout(ctx, q, opssql.DefaultTimeout)
			if qerr != nil {
				return opssql.Result{}, qerr
			}
			return opssql.Result{Columns: res.Columns, Rows: res.Rows}, nil
		})
		if cerr := l.Close(); ferr == nil && cerr != nil {
			return errs.Wrap(errs.Internal, "wire: close the lens", cerr)
		}
		return ferr
	})
}
