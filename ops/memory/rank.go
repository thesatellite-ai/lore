package memory

// rank.go — the one full-text matcher of `lore search` and every `<kind>
// search` (D-461). Before, a kind's search handed the query to FTS5 as
// written, so `lore decision search "how do we deploy orbit"` needed
// every word, stop words too, and found nothing that `lore search`
// found; its hits came in bm25 order with no title share.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/querywords"
	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// MatchFunc runs one FTS5 text over a table, at most limit hits, best
// first (Env.Search, or its column-restricted form).
type MatchFunc func(ctx context.Context, match string, limit int) ([]Hit, error)

// Match runs w's search through match and, for a natural query, each
// topic word's own search: it returns the hits (the any-word hits in
// bm25 order, then rows only a word's search found) and how many words
// each row holds by the index. Plain words FTS5 refuses are retried once
// with every word quoted (searchrank.Words.Retry); a query written in
// FTS5 syntax that FTS5 refuses, or a second refusal, is returned as is
// (E_INVALID_INPUT with the reason, D-475).
// Shared by `lore search` (wire) and SearchFields.
func Match(ctx context.Context, match MatchFunc, w searchrank.Words, limit int) ([]Hit, map[ids.AnyID]int, error) {
	hits, err := match(ctx, w.FTS, limit)
	if errs.Has(err, errs.InvalidInput) && w.Retry() {
		if quoted := searchrank.Quoted(w.FTS); quoted != "" && quoted != w.FTS {
			hits, err = match(ctx, quoted, limit)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	matched := map[ids.AnyID]int{}
	if !w.Natural {
		return hits, matched, nil
	}
	found := make(map[ids.AnyID]bool, len(hits))
	for _, h := range hits {
		found[h.ID] = true
	}
	for _, term := range w.Terms {
		th, terr := match(ctx, w.TermFTS(term), limit)
		if errs.Has(terr, errs.InvalidInput) {
			continue // a word FTS5 cannot read on its own counts nowhere
		}
		if terr != nil {
			return nil, nil, terr
		}
		for _, h := range th {
			matched[h.ID]++
			if !found[h.ID] {
				found[h.ID] = true
				hits = append(hits, h)
			}
		}
	}
	return hits, matched, nil
}

// Held is how many of w's words a row holds: its count from Match, or
// more when its own text (the fields searched) holds more — a word's
// search returns at most its limit, so in a big store a row holding a
// common word can be past that cut.
func Held(w searchrank.Words, byIndex int, text string) int {
	if !w.Natural {
		return byIndex
	}
	return max(byIndex, querywords.Coverage(text, w.Terms))
}

// RankEnv is what a ranked search reads: the cache search and the rows
// (Env has both; so does the comment noun's Env).
type RankEnv interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Search(ctx context.Context, table row.Table, match string, limit int) ([]Hit, error)
}

// SearchRanked is SearchFields over every full-text field of table and
// every store, for a noun whose Env is not this package's (comments).
func SearchRanked(ctx context.Context, env RankEnv, noun string, table row.Table, match string, limit int) ([]Hit, error) {
	return searchFields(ctx, env, noun, table, match, nil, limit)
}

// Row fields that name a row for ranking (searchrank.Label).
const (
	labelTitle = "title"
	labelName  = "name" // playbooks, workflows, … are named, not titled
)

// rankHits orders hits of table (from Match) as `lore search` does
// (searchrank.Words.Order) and sets each Score to its relevance in
// [0, 1). fields are the full-text fields the search read (their text
// counts toward the words held). Rows are read per store with env.List,
// once each; a hit whose row is gone keeps its place at the end.
func rankHits(ctx context.Context, env RankEnv, table row.Table, w searchrank.Words, hits []Hit, matched map[ids.AnyID]int, fields []string, limit int) ([]Hit, error) {
	rows := map[store.Source]map[ids.AnyID]row.Row{}
	items := make([]searchrank.Item, len(hits))
	for i, h := range hits {
		byID, ok := rows[h.Source]
		if !ok {
			list, err := env.List(ctx, h.Source, table)
			if err != nil {
				return nil, err
			}
			byID = make(map[ids.AnyID]row.Row, len(list))
			for _, r := range list {
				byID[r.ID] = r
			}
			rows[h.Source] = byID
		}
		r := byID[h.ID]
		var text strings.Builder
		for _, f := range fields {
			text.WriteString(field(r, f) + "\n")
		}
		title := field(r, labelTitle)
		if title == "" {
			title = field(r, labelName)
		}
		items[i] = searchrank.Item{Label: searchrank.Label(title, field(r, fieldBody)), Matched: Held(w, matched[h.ID], text.String())}
	}
	ranked := w.Order(items, limit)
	out := make([]Hit, len(ranked))
	for i, rk := range ranked {
		out[i] = hits[rk.Index]
		out[i].Score = rk.Score
	}
	return out, nil
}
