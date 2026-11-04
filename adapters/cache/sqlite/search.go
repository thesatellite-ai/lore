package sqlite

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// Search runs an FTS5 MATCH over table's full-text fields and returns the
// matching row ids, best first (FTS5 rank), at most limit. Columns in a
// column filter are named "f_<field>" (see the package doc).
func (c *Cache) Search(ctx context.Context, table row.Table, match string, limit int) ([]ids.AnyID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	p, ok := c.st.plans[table]
	if !ok {
		return nil, errs.New(errs.NotFound, "sqlite cache: no table "+quoteText(table.String()))
	}
	if p.fts == "" {
		return nil, errs.New(errs.Unsupported, "sqlite cache: table "+table.String()+" has no full-text fields")
	}
	if limit <= 0 {
		return nil, errs.New(errs.InvalidInput, "sqlite cache: search limit must be positive")
	}
	rs, err := c.db.QueryContext(ctx, p.sql.search, match, limit)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "sqlite cache: search", err)
	}
	defer func() { _ = rs.Close() }()
	var out []ids.AnyID
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			return nil, dbErr("search", err)
		}
		id, err := ids.ParseAny(s)
		if err != nil {
			return nil, errs.Wrap(errs.DBCorrupt, "sqlite cache: bad id in search index", err)
		}
		out = append(out, id)
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("search", err)
	}
	return out, nil
}

// ColumnMatch restricts match to the full-text fields named in fields: the
// FTS5 column filter `{f_a f_b} : (match)` over this cache's column names
// (ftsColPrefix + field). match is returned unchanged when fields is empty.
func ColumnMatch(fields []string, match string) string {
	if len(fields) == 0 {
		return match
	}
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = ftsColPrefix + f
	}
	return "{" + strings.Join(cols, " ") + "} : (" + match + ")"
}
