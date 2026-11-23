package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
)

var _ engine.LastGoodKinds = (*caches)(nil)

// LastGoodKind implements engine.LastGoodKinds by routing to source's
// cache file (RES 3.13, OV35-LASTGOOD).
func (c *caches) LastGoodKind(ctx context.Context, source store.Source, kindID string) (row.Row, []row.Row, bool, error) {
	f, key, err := c.at(source)
	if err != nil {
		return row.Row{}, nil, false, err
	}
	return f.LastGoodKind(ctx, key, kindID)
}

// LastGoodKind is the ops/settings lastGooder Env upgrade: the engine's
// LastGoodKind (RES 3.13; resource read ops fall back to it, p2-res).
func (a *App) LastGoodKind(ctx context.Context, source store.Source, kindID string) (row.Row, []row.Row, bool, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return row.Row{}, nil, false, err
	}
	return eng.LastGoodKind(ctx, source, kindID)
}

// referrers reads row_refs for refIDs in every distinct cache file (a
// file opened under several names is read once; the caller keeps each
// line once).
func (c *caches) referrers(ctx context.Context, refIDs []string) ([]sqlite.Referrer, error) {
	c.mu.Lock()
	var files []*sqlite.Cache
	seen := map[*sqlite.Cache]bool{}
	for _, s := range c.order {
		if f := c.by[s]; f != nil && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	c.mu.Unlock()
	var out []sqlite.Referrer
	for _, f := range files {
		got, err := f.Referrers(ctx, refIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

var _ opssettings.RefFinder = (*App)(nil)

// RowsReferencing implements opssettings.RefFinder: the rows whose text
// references one of refIDs (row_refs), for resource graph --up and
// resource impact (devco D-764).
func (a *App) RowsReferencing(ctx context.Context, refIDs []string) ([]opssettings.RefRow, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	got, err := st.caches.referrers(ctx, refIDs)
	if err != nil {
		return nil, err
	}
	out := make([]opssettings.RefRow, len(got))
	for i, r := range got {
		out[i] = opssettings.RefRow{Table: r.Table, ID: r.ID, Field: r.Field, RefID: r.RefID}
	}
	return out, nil
}
