package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// MoveWith implements opsrow.Mover: App.Move with the move options
// (--drop-internal, --with-time; CLI_REFERENCE 2.7), behind the same
// guards (unreachable target, O6 format check, actor row first).
func (a *App) MoveWith(ctx context.Context, ref engine.Ref, to store.Source, opt engine.MoveOptions) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(to)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(to); ferr != nil {
		return engine.Written{}, ferr
	}
	// batch leftover-3: staged with the copy, as App.Move
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActor(ctx, st, to); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.MoveWith(ctx, ref, to, opt)
		a.wroteTo(to, err)
		return w, err
	})
}
