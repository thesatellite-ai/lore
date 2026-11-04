package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsmigrate.Rewriter = (*App)(nil)

// RewriteV1 implements opsmigrate.Rewriter on the repo store. Each v1 row
// is re-encoded exactly as model/row upcast it (same fields, same audit
// times, the _hlc derived from updated_at) and written with Expect set to
// that HLC, so a row someone changed meanwhile is refused (E_STALE), never
// overwritten. The rewrite bypasses the engine's Update on purpose: Update
// would stamp a new HLC and updated_at, which is an edit, not a format
// change. The cache is refreshed afterwards.
func (a *App) RewriteV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	repo := st.backends[0] // open() attaches the repo store first
	rows, _, err := repo.Read(ctx, "")
	if err != nil {
		return nil, err
	}
	var found []ids.AnyID
	var changes []store.Change
	for _, sr := range rows {
		if sr.Table != table {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			return nil, err
		}
		if r.SourceVersion == row.FormatVersion {
			continue
		}
		found = append(found, r.ID)
		up, err := store.FromRow(r)
		if err != nil {
			return nil, err
		}
		changes = append(changes, store.Change{Row: up, Expect: sr.HLC})
	}
	if dryRun || len(changes) == 0 {
		return found, nil
	}
	if err := repo.Write(ctx, changes); err != nil {
		return nil, err
	}
	a.wrote(nil)
	if _, err := st.eng.Refresh(ctx, store.SourceRepo); err != nil {
		return nil, err
	}
	return found, nil
}
