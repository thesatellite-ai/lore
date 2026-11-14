package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/value"
)

// RewriteOrigin implements opsinfra.RepoFiles: engine.RewriteOrigin behind
// the App's write path (format check, target check, actor row first), as
// Update does. Only `lore repo merge-ids` calls it (PROVENANCE_DESIGN 11.9).
func (a *App) RewriteOrigin(ctx context.Context, ref engine.Ref, fn func(value.JSON) (value.JSON, bool, error)) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	src := refStore(ctx, st, ref)
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(src)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(src); ferr != nil {
		return engine.Written{}, ferr
	}
	// batch leftover-3: the actor row and the rewrite as one batch
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.RewriteOrigin(ctx, ref, fn)
		a.wroteTo(src, err)
		return w, err
	})
}
