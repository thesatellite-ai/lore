package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// SearchHit is one full-text match: the row id, its score and a snippet of
// the matched text (matches in <b>…</b>).
//
// Score is SQLite's weighted bm25 (per field by schema.TableDef.FTSWeightOf)
// with the sign flipped, as v0.1.17's fts5 package printed it: higher is
// better, a match is positive. Hits come best first either way.
type SearchHit struct {
	ID      ids.AnyID
	Score   float64
	Snippet string
}

// Search runs an FTS5 MATCH over table's full-text fields and returns the
// matching row ids, best first (weighted bm25), at most limit. Columns in
// a column filter are named "f_<field>" (see the package doc).
func (c *Cache) Search(ctx context.Context, table row.Table, match string, limit int) ([]ids.AnyID, error) {
	hits, err := c.SearchHits(ctx, table, match, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ids.AnyID, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out, nil
}

// SearchHits is Search with each hit's score and snippet.
func (c *Cache) SearchHits(ctx context.Context, table row.Table, match string, limit int) ([]SearchHit, error) {
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
	var out []SearchHit
	for rs.Next() {
		var s string
		var h SearchHit
		var snip sql.NullString
		if err := rs.Scan(&s, &h.Score, &snip); err != nil {
			return nil, dbErr("search", err)
		}
		id, err := ids.ParseAny(s)
		if err != nil {
			return nil, errs.Wrap(errs.DBCorrupt, "sqlite cache: bad id in search index", err)
		}
		// SQLite's bm25 is lower-is-better (negative for a match); flip it
		// to v0.1.17's higher-is-better score.
		h.ID, h.Snippet, h.Score = id, snip.String, -h.Score
		out = append(out, h)
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
