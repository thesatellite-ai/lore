package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// StoreFresh is the fetch stamp of an attached store (RES 3.6
// resources.stale_after, P2-RES-7): the backend's store.Freshness, or
// Known false for a backend without one (a folder store has no remote).
func (e *Engine) StoreFresh(ctx context.Context, source store.Source) (store.Fresh, error) {
	a, err := e.attached(source)
	if err != nil {
		return store.Fresh{}, err
	}
	f, ok := a.Backend.(store.Freshness)
	if !ok {
		return store.Fresh{}, nil
	}
	return f.Fresh(ctx)
}
