package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// PlanSecretsNamed implements opstask.SecretPlanner: a task import's
// secret pre-scan over every record before the first write (devco
// D-717), in the engine that writes to target, with the caller's flags.
// Nothing is written.
func (a *App) PlanSecretsNamed(ctx context.Context, verb, rerun string, target store.Source, rows []row.Row, names map[ids.AnyID]string) (engine.SecretPlan, error) {
	st, err := a.state(ctx)
	if err != nil {
		return engine.SecretPlan{}, err
	}
	target = st.eng.StoreName(target)
	if st, err = a.writeStateFor(ctx, target); err != nil {
		return engine.SecretPlan{}, err
	}
	return st.eng.PlanSecretsNamed(ctx, verb, rerun, target, rows, names)
}

// SecretsForRow implements opstask.SecretPlanner: ctx with the caller's
// secret answer narrowed to row id's part of the plan (engine.ForRow).
func (a *App) SecretsForRow(ctx context.Context, p engine.SecretPlan, id ids.AnyID) context.Context {
	st, err := a.state(ctx)
	if err != nil {
		return ctx // no engine: the create screens the row with the whole answer
	}
	return st.eng.ForRow(ctx, p, id)
}
