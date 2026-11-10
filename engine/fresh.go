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
	// A wrapper (the CLI's push-after-write layer) unwraps to the concrete
	// backend; the first layer that implements Freshness answers.
	for b := a.Backend; b != nil; {
		if f, ok := b.(store.Freshness); ok {
			return f.Fresh(ctx)
		}
		u, ok := b.(unwrapper)
		if !ok {
			break
		}
		b = u.Unwrap()
	}
	return store.Fresh{}, nil
}
