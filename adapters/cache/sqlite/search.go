package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

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
	out, ftsErr := c.ftsHits(ctx, p, match, limit)
	// Second column (SECRETS_AND_REFERENCES 9, 8.7): the filled-in text of
	// row_expanded, so `db.hub.acme.example` finds a row that says
	// ${var:db_host}. Its hits rank after every full-text hit (score 0).
	more, expErr := c.expandedHits(ctx, table, match, limit-len(out), out)
	if ftsErr != nil {
		if len(more) == 0 {
			return nil, ftsErr
		}
		return more, nil
	}
	if expErr != nil {
		return nil, expErr
	}
	return append(out, more...), nil
}

// ftsHits is the FTS5 MATCH of p.
func (c *Cache) ftsHits(ctx context.Context, p plan, match string, limit int) ([]SearchHit, error) {
	rs, err := c.db.QueryContext(ctx, p.sql.search, match, limit)
	if err != nil {
		if reason, ok := ftsQueryReason(err); ok {
			return nil, errs.Wrap(errs.InvalidInput, "search "+quoteText(match)+": not a valid search query ("+reason+")", err).
				WithHint(ftsQueryHint)
		}
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

// expandedHits finds rows of table whose row_expanded text holds every
// plain term of match (case-insensitive for ASCII), skipping
// ids already in have. A match using FTS syntax (column filters, groups,
// NEAR) gets no second-column hits: its meaning has no substring form.
func (c *Cache) expandedHits(ctx context.Context, table row.Table, match string, limit int, have []SearchHit) ([]SearchHit, error) {
	terms, ok := plainTerms(match)
	if !ok || limit <= 0 {
		return nil, nil
	}
	termsJSON, err := json.Marshal(terms)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "sqlite cache: encode search terms", err)
	}
	// every term must occur (lower() folds ASCII, as LIKE would)
	q := `SELECT entity_id, text FROM "` + tblExpanded + `" WHERE entity_table = ? AND NOT EXISTS ` +
		`(SELECT 1 FROM json_each(?) t WHERE instr(lower(text), lower(t.value)) = 0) ORDER BY entity_id`
	rs, err := c.db.QueryContext(ctx, q, table.String(), string(termsJSON))
	if err != nil {
		return nil, dbErr("search expanded text", err)
	}
	defer func() { _ = rs.Close() }()
	seen := map[string]bool{}
	for _, h := range have {
		seen[h.ID.String()] = true
	}
	var out []SearchHit
	for rs.Next() && len(out) < limit {
		var s, text string
		if err := rs.Scan(&s, &text); err != nil {
			return nil, dbErr("search expanded text", err)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		id, err := ids.ParseAny(s)
		if err != nil {
			return nil, errs.Wrap(errs.DBCorrupt, "sqlite cache: bad id in row_expanded", err)
		}
		out = append(out, SearchHit{ID: id, Snippet: snippetOf(text, terms[0])})
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("search expanded text", err)
	}
	return out, nil
}

// plainTerms splits a match into its words when it uses no FTS syntax
// beyond quotes, a trailing * and AND; false otherwise.
func plainTerms(match string) ([]string, bool) {
	if strings.ContainsAny(match, "{}():^") {
		return nil, false
	}
	var out []string
	for _, w := range strings.Fields(match) {
		switch w {
		case "AND":
			continue
		case "OR", "NOT", "NEAR":
			return nil, false
		}
		w = strings.TrimSuffix(strings.Trim(w, `"`), "*")
		if w != "" {
			out = append(out, w)
		}
	}
	return out, len(out) > 0
}

// snippetOf is up to 40 bytes either side of the first match of term,
// with the match in <b>…</b> like the FTS snippets. Every cut lands on a
// UTF-8 character boundary, and the match offset is taken from the
// lower-cased text only when lower-casing kept every byte offset (it does
// not for some letters, e.g. U+0130), so the slices never split a
// character or run past the text.
func snippetOf(text, term string) string {
	const side = 40
	hay, needle := text, term
	if lt := strings.ToLower(text); len(lt) == len(text) {
		hay, needle = lt, strings.ToLower(term)
	}
	i := strings.Index(hay, needle)
	if i < 0 {
		return text[:runeFloor(text, min(len(text), 2*side))]
	}
	end := i + len(needle)
	lo, hi := runeCeil(text, max(0, i-side)), runeFloor(text, min(len(text), end+side))
	return text[lo:i] + "<b>" + text[i:end] + "</b>" + text[end:hi]
}

// runeFloor is the largest character boundary of s at or before n.
func runeFloor(s string, n int) int {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// runeCeil is the smallest character boundary of s at or after n.
func runeCeil(s string, n int) int {
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return n
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

// ftsQueryHint is the next step after a query the full-text syntax
// refuses: the syntax a person can use instead.
const ftsQueryHint = `search words as typed; "two words" in double quotes for a phrase, word* for a prefix, AND / OR / NOT between words; a lone quote, ( or * is not a query`

// ftsQueryReason is FTS5's own reason when err is a refusal of the query
// text (a lone quote, a dangling AND, an unknown column filter), so the
// person reads what was wrong with their words. Before, every such query
// printed "sqlite cache: search" with no reason and no hint (campaign
// knowledge 2026-11-08). Other errors (the database itself) are not
// query errors and return false.
func ftsQueryReason(err error) (string, bool) {
	msg := err.Error()
	for _, marker := range []string{"fts5: ", "unterminated string", "no such column", "unknown special query"} {
		if i := strings.Index(msg, marker); i >= 0 {
			reason := strings.TrimSpace(msg[i:])
			if j := strings.LastIndex(reason, " ("); j > 0 && strings.HasSuffix(reason, ")") {
				reason = reason[:j] // drop SQLite's numeric result code
			}
			return reason, true
		}
	}
	return "", false
}
