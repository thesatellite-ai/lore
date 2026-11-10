package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// LastGoodKinds is the optional cache capability of RES 3.13
// (OV35-LASTGOOD): the last definition of a resource kind (its kinds row
// and kind_fields rows) seen with no conflict markers. The resource ops
// fall back to it, read-only, while a kind or field row holds a conflict.
type LastGoodKinds interface {
	LastGoodKind(ctx context.Context, source store.Source, kindID string) (kind row.Row, fields []row.Row, ok bool, err error)
}

// LastGoodKind asks the cache for kindID's last good definition in
// source; ok is false when the cache has none (never seen good here, or
// a cache without the capability).
func (e *Engine) LastGoodKind(ctx context.Context, source store.Source, kindID string) (row.Row, []row.Row, bool, error) {
	lg, ok := e.cache.(LastGoodKinds)
	if !ok {
		return row.Row{}, nil, false, nil
	}
	return lg.LastGoodKind(ctx, source, kindID)
}
