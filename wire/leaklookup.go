package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
)

// LocateHidden implements rowref's hidden-row lookup (engine.LocateHidden):
// a row in a data folder of an attached store that this checkout does not
// attach, so F15 can refuse a pointer at it (E_LEAK_BLOCKED).
func (a *App) LocateHidden(ctx context.Context, ref engine.Ref) (engine.HiddenHit, bool, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return engine.HiddenHit{}, false, err
	}
	return eng.LocateHidden(ctx, ref)
}
