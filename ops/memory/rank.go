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
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// MatchFunc runs one FTS5 text over a table, at most limit hits, best
// first (Env.Search, or its column-restricted form).
type MatchFunc func(ctx context.Context, match string, limit int) ([]Hit, error)

// Match runs w's search through match and, for a natural query, each
// topic word's own search: it returns the hits (the any-word hits in
// bm25 order, then rows only a word's search found), how many words each
// row holds by the index, and the words the hits were found with (the
// caller ranks them with these). A query written in FTS5 syntax that
// FTS5 refuses is searched again as its plain words when it holds a
// topic word (searchrank.Words.Plain: `alice (person)`, `orbit)`, P-14);
// plain words FTS5 refuses are retried once with every word quoted
// (searchrank.Words.Retry). Syntax with no topic word (`"`, `(`, `AND`),
// or a refusal after both, is returned as is (E_INVALID_INPUT with the
// reason, D-475). Shared by `lore search` (wire) and SearchFields.
func Match(ctx context.Context, match MatchFunc, w searchrank.Words, limit int) ([]Hit, map[ids.AnyID]int, searchrank.Words, error) {
	return MatchCounting(ctx, match, nil, w, limit)
}

// MatchCounting is Match with bare, a MatchFunc that returns the same
// hits without snippets (nil: none), for each topic word's own search.
// Why (devco:D-808): those searches only count which rows hold a word,
// yet each built a snippet for up to limit rows. When the any-word
// search was not cut at limit, every row a word finds is already a hit,
// so the word's search runs bare; should it find a row that is not (a
// word's search the any-word one does not cover), it runs again with
// snippets, so the hits and counts are Match's exactly.
func MatchCounting(ctx context.Context, match, bare MatchFunc, w searchrank.Words, limit int) ([]Hit, map[ids.AnyID]int, searchrank.Words, error) {
	hits, words, w, err := MatchWords(ctx, match, bare, w, limit)
	if err != nil {
		return nil, nil, w, err
	}
	matched := make(map[ids.AnyID]int, len(words.Held))
	for id, held := range words.Held {
		for _, h := range held {
			if h {
				matched[id]++
			}
		}
	}
	return hits, matched, w, nil
}

// WordHits is which of a natural query's topic words each row holds by
// its words' own searches (MatchWords): Held[id][k] for Terms[k], and DF[k]
// how many rows word k's search found (at most the search's limit), the
// counts searchrank.Share weighs a word's rarity by (devco:D-1007).
type WordHits struct {
	Held map[ids.AnyID][]bool
	DF   []int
}

// MatchWords is MatchCounting keeping which words each row holds and how
// many rows each word's search found (WordHits), not only a count per
// row: lore search weighs a stale fact's and a card's words by their
// rarity (devco:D-1007). Empty WordHits for a query that is not Natural.
func MatchWords(ctx context.Context, match, bare MatchFunc, w searchrank.Words, limit int) ([]Hit, WordHits, searchrank.Words, error) {
	var words WordHits
	hits, err := match(ctx, w.FTS, limit)
	if errs.Has(err, errs.InvalidInput) {
		if plain, ok := w.Plain(); ok {
			w = plain
			hits, err = match(ctx, w.FTS, limit)
		}
	}
	if errs.Has(err, errs.InvalidInput) && w.Retry() {
		if quoted := searchrank.Quoted(w.FTS); quoted != "" && quoted != w.FTS {
			hits, err = match(ctx, quoted, limit)
		}
	}
	if err != nil {
		return nil, words, w, err
	}
	words.Held = map[ids.AnyID][]bool{}
	if !w.Natural {
		return hits, words, w, nil
	}
	words.DF = make([]int, len(w.Terms))
	found := make(map[ids.AnyID]bool, len(hits))
	for _, h := range hits {
		found[h.ID] = true
	}
	if len(hits) >= limit {
		bare = nil // the any-word search was cut: a word can find rows past it
	}
	for k, term := range w.Terms {
		th, terr := termHits(ctx, match, bare, w.TermFTS(term), limit, found)
		if errs.Has(terr, errs.InvalidInput) {
			continue // a word FTS5 cannot read on its own counts nowhere
		}
		if terr != nil {
			return nil, WordHits{}, w, terr
		}
		words.DF[k] = len(th)
		for _, h := range th {
			held := words.Held[h.ID]
			if held == nil {
				held = make([]bool, len(w.Terms))
				words.Held[h.ID] = held
			}
			held[k] = true
			if !found[h.ID] {
				found[h.ID] = true
				hits = append(hits, h)
			}
		}
	}
	return hits, words, w, nil
}

// termHits is one topic word's search: bare when it is set and finds no
// row outside found, else match (see MatchCounting).
func termHits(ctx context.Context, match, bare MatchFunc, q string, limit int, found map[ids.AnyID]bool) ([]Hit, error) {
	if bare == nil {
		return match(ctx, q, limit)
	}
	th, err := bare(ctx, q, limit)
	if err != nil {
		return th, err
	}
	for _, h := range th {
		if !found[h.ID] {
			return match(ctx, q, limit)
		}
	}
	return th, nil
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
		items[i] = searchrank.Item{Label: searchrank.Label(title, field(r, fieldBody)), Matched: Held(w, matched[h.ID], text.String()), At: schema.RowDate(r)}
	}
	ranked := w.Order(items, limit)
	out := make([]Hit, len(ranked))
	for i, rk := range ranked {
		out[i] = hits[rk.Index]
		out[i].Score = rk.Score
	}
	return out, nil
}
