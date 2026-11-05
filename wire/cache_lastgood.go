package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

var _ engine.LastGoodKinds = (*caches)(nil)

// LastGoodKind implements engine.LastGoodKinds by routing to source's
// cache file (RES 3.13, OV35-LASTGOOD).
func (c *caches) LastGoodKind(ctx context.Context, source store.Source, kindID string) (row.Row, []row.Row, bool, error) {
	f, err := c.of(source)
	if err != nil {
		return row.Row{}, nil, false, err
	}
	return f.LastGoodKind(ctx, source, kindID)
}
