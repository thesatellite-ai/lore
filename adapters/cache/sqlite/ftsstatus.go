package sqlite

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// FTSCount is one table's full-text index health in one cache file, for
// `lore search status` (P3-MISC-1): Rows typed rows of the source, Indexed
// rows of its FTS5 table. A table without full-text fields has HasFTS false.
type FTSCount struct {
	FTSTable string
	HasFTS   bool
	Rows     int
	Indexed  int
}

// FTSCounts counts table's typed rows and FTS rows of source. A table this
// cache does not hold is E_NOT_FOUND.
//
// Why both counts: the cache writes a typed row and its FTS row in one
// transaction (project.go), so they only differ when the file was damaged
// or written by an older build; a difference is the drift `search rebuild`
// clears.
func (c *Cache) FTSCounts(ctx context.Context, source store.Source, table row.Table) (FTSCount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return FTSCount{}, err
	}
	p, ok := c.st.plans[table]
	if !ok {
		return FTSCount{}, errs.New(errs.NotFound, "sqlite cache: no table "+quoteText(table.String()))
	}
	tq, err := ident(p.def.Name.String())
	if err != nil {
		return FTSCount{}, err
	}
	out := FTSCount{FTSTable: p.fts, HasFTS: p.fts != ""}
	if scanErr := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tq+` WHERE "`+colSource+`" = ?`, string(source)).Scan(&out.Rows); scanErr != nil {
		return FTSCount{}, dbErr("count rows", scanErr)
	}
	if !out.HasFTS {
		return out, nil
	}
	fq, err := ident(p.fts)
	if err != nil {
		return FTSCount{}, err
	}
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+fq+` WHERE "`+colSource+`" = ?`, string(source)).Scan(&out.Indexed); err != nil {
		return FTSCount{}, dbErr("count fts rows", err)
	}
	return out, nil
}
