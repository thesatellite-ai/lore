package wire

// The trash noun's Env (ops/trash, DELETION_DESIGN 3.3-3.5): reads go to the
// engine as they are; restore and purge are writes, so they pass the same
// format check as every write (writeState) and record the landed write
// (wroteTo) for each store they touched.

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// Trashed implements opstrash.Env.
func (a *App) Trashed(ctx context.Context) ([]engine.Trashed, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return eng.Trashed(ctx)
}

// TrashTarget implements opstrash.Env.
func (a *App) TrashTarget(ctx context.Context, target string) (engine.TrashResult, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return engine.TrashResult{}, err
	}
	return eng.TrashTarget(ctx, target)
}

// InTrash implements the lookup.Trasher upgrade (DELETION 3.2): E_IN_TRASH
// for a row in the trash of an attached store, else nil.
func (a *App) InTrash(ctx context.Context, ref engine.Ref) error {
	eng, err := a.Engine(ctx)
	if err != nil {
		return err
	}
	return eng.InTrash(ctx, ref)
}

// TableSchema implements opstrash.Env.
func (a *App) TableSchema(ctx context.Context, table row.Table) (schema.TableDef, bool) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return schema.TableDef{}, false
	}
	return eng.Registry().Table(table)
}

// Restore implements opstrash.Env.
func (a *App) Restore(ctx context.Context, target string) (engine.TrashResult, error) {
	return a.trashWrite(ctx, func(eng *engine.Engine) (engine.TrashResult, error) { return eng.Restore(ctx, target) })
}

// PurgeTrash implements opstrash.Env.
func (a *App) PurgeTrash(ctx context.Context, target string) (engine.TrashResult, error) {
	return a.trashWrite(ctx, func(eng *engine.Engine) (engine.TrashResult, error) { return eng.PurgeTrash(ctx, target) })
}

// PurgeOlder implements opstrash.Env.
func (a *App) PurgeOlder(ctx context.Context, cutoff time.Time, expired, dryRun bool) (engine.TrashResult, error) {
	if dryRun {
		eng, err := a.Engine(ctx)
		if err != nil {
			return engine.TrashResult{}, err
		}
		return eng.PurgeOlder(ctx, cutoff, expired, true)
	}
	return a.trashWrite(ctx, func(eng *engine.Engine) (engine.TrashResult, error) {
		return eng.PurgeOlder(ctx, cutoff, expired, false)
	})
}

// trashWrite runs one trash write after the write checks, then records the
// stores it landed in.
func (a *App) trashWrite(ctx context.Context, do func(*engine.Engine) (engine.TrashResult, error)) (engine.TrashResult, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.TrashResult{}, err
	}
	res, err := do(st.eng)
	if len(res.Rows) == 0 {
		a.wroteTo("", err)
	}
	for _, t := range res.Rows {
		a.wroteTo(t.Store, err)
	}
	return res, err
}
