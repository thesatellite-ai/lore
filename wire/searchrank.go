package wire

// searchrank.go — how `lore search` reads free text and ranks its hits:
// logic/searchrank plans and scores, opsmemory.Match runs the any-word
// search and each word's own search (D-435, D-436). Every `<kind> search`
// goes through the same two (opsmemory.SearchFields, D-461), so a question
// finds the same rows in the same order through either.

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/searchrank"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// scoreInOrder scores hits that matched every term (condition searches:
// meta key:value, resource field:value), keeping their order as rank.
func scoreInOrder(hits []opssearch.Hit, terms []string) {
	for i := range hits {
		hits[i].Score = searchrank.Score(len(terms), len(terms), searchrank.Names(hits[i].Title, terms), searchrank.TitleShare(hits[i].Title, terms), i)
	}
}

// resourceKindNames maps every kind id the attached caches hold to its
// name, for the resource hits' kind (D-436); the nearest cache (repo
// first, then the stores in attach order) wins a shared id. devco:D-808:
// read from each cache's kinds table, not through the SQL lens, which
// created every lens view on each search.
func (a *App) resourceKindNames(ctx context.Context) (map[string]string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return st.caches.kindNames(ctx)
}
