package wire

// The trash noun's Env (ops/trash, DELETION_DESIGN 3.3-3.5): reads go to the
// engine as they are; restore and purge are writes, so they pass the same
// format check as every write (writeState) and record the landed write
// (wroteTo) for each store they touched.

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/row"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
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

// PreviewDelete is the group a delete of refs would move to the trash,
// planned without writing (r3-projdel): plan and project delete name the
// counts and refuse before their first write.
func (a *App) PreviewDelete(ctx context.Context, refs ...engine.Ref) (engine.DeletePreview, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return engine.DeletePreview{}, err
	}
	return eng.PreviewDelete(ctx, refs...)
}

// TableSchema implements opstrash.Env.
func (a *App) TableSchema(ctx context.Context, table row.Table) (schema.TableDef, bool) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return schema.TableDef{}, false
	}
	return eng.Registry().Table(table)
}

// Restore implements opstrash.Env. Before the engine writes anything, every
// row coming back into the repo store passes the leak check a create there
// gets (DELETION 3.3 step 3 and 3.11, P3-DEL-1): restoreGuard.
func (a *App) Restore(ctx context.Context, target string) (engine.TrashResult, error) {
	return a.trashWriteChecked(ctx, func(eng *engine.Engine) (engine.TrashResult, error) { return eng.TrashTarget(ctx, target) },
		func(eng *engine.Engine) (engine.TrashResult, error) { return eng.Restore(ctx, target) }, a.restoreGuard)
}

// restoreGuard runs the client-repo leak check of a create on each planned
// row of a restore that lives in the repo store: the same
// unreachableGuard (fail closed while an org store cannot be read) and
// Options.GuardRepoWrite with the row's text as it comes back (delete
// fields cleared). explicit is true: the row already lives in this repo,
// so the restore never re-routes it (the routing default of a create does
// not apply), while the leak check still runs. Rows in other stores pass,
// as a create there does. A refusal leaves the whole batch in the trash.
func (a *App) restoreGuard(ctx context.Context, st *state, planned engine.TrashResult) error {
	for _, t := range planned.Rows {
		if t.Store != store.SourceRepo && t.Store != "" {
			continue
		}
		if err := a.unreachableGuard(ctx, st, store.SourceRepo); err != nil {
			return err
		}
		q, err := a.writeQuery(st, t.Row.Table, trash.Clear(t.Row).Fields)
		if err != nil {
			return err
		}
		if err = a.guard(ctx, q, true); err != nil {
			return err
		}
	}
	return nil
}

// PurgeTrash implements opstrash.Env.
func (a *App) PurgeTrash(ctx context.Context, target string) (engine.TrashResult, error) {
	return a.trashWrite(ctx, func(eng *engine.Engine) (engine.TrashResult, error) { return eng.TrashTarget(ctx, target) },
		func(eng *engine.Engine) (engine.TrashResult, error) { return eng.PurgeTrash(ctx, target) })
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
		return eng.PurgeOlder(ctx, cutoff, expired, true)
	}, func(eng *engine.Engine) (engine.TrashResult, error) {
		return eng.PurgeOlder(ctx, cutoff, expired, false)
	})
}

// trashWrite runs one trash write after the write checks, then records the
// stores it landed in. plan names the rows the write will touch without
// writing (the restore or purge target, a purge dry run): the actor row is
// written into each of their stores first, as every other write does, so
// the updated_by of a restore or a tombstone always resolves there (OV9).
func (a *App) trashWrite(ctx context.Context, plan, do func(*engine.Engine) (engine.TrashResult, error)) (engine.TrashResult, error) {
	return a.trashWriteChecked(ctx, plan, do, nil)
}

// trashWriteChecked is trashWrite with check, which (when set) vets the
// planned rows before anything is written (the restore's leak check); an
// error refuses the write.
func (a *App) trashWriteChecked(ctx context.Context, plan, do func(*engine.Engine) (engine.TrashResult, error),
	check func(context.Context, *state, engine.TrashResult) error) (engine.TrashResult, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.TrashResult{}, err
	}
	if planned, perr := plan(st.eng); perr == nil { // a bad target is reported by the write itself
		if check != nil {
			if cerr := check(ctx, st, planned); cerr != nil {
				return engine.TrashResult{}, cerr
			}
		}
		done := map[store.Source]bool{}
		for _, t := range planned.Rows {
			if done[t.Store] {
				continue
			}
			done[t.Store] = true
			if aerr := a.ensureActor(ctx, st, t.Store); aerr != nil {
				return engine.TrashResult{}, aerr
			}
		}
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

// lensTrash is the rows of the `lore sql` trash view (DELETION 3.2, 2.9):
// every row in the trash of the attached stores, a sensitive title masked
// unless --show-sensitive.
func (a *App) lensTrash(ctx context.Context, mask bool) ([]sqlite.TrashRow, error) {
	all, err := a.Trashed(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sqlite.TrashRow, 0, len(all))
	for _, t := range all {
		def, _ := a.TableSchema(ctx, t.Row.Table)
		r := sqlite.TrashRow{Source: string(t.Store), Table: t.Row.Table.String(), ID: t.Row.ID.String(),
			Title: opstrash.Title(t.Row, def, mask), DeletedBy: t.Info.By, Reason: t.Info.Reason, Batch: t.Info.Batch, Dependent: t.Dependent}
		if !t.Info.At.IsZero() {
			r.DeletedAt = t.Info.At.UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out, nil
}

// TrashStats implements opstrash.Env.
func (a *App) TrashStats(ctx context.Context) (engine.TrashStats, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return engine.TrashStats{}, err
	}
	return eng.TrashStats(ctx)
}

// Overruled implements opstrash.Env.
func (a *App) Overruled(ctx context.Context) ([]engine.Overruled, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return eng.Overruled(ctx)
}
