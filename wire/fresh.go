package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// StoreFresh is the ops/settings freshener Env upgrade: the engine's
// StoreFresh (RES 3.6 resources.stale_after, P2-RES-7).
func (a *App) StoreFresh(ctx context.Context, source store.Source) (store.Fresh, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return store.Fresh{}, err
	}
	return eng.StoreFresh(ctx, source)
}
