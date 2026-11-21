package wire

// searchrank.go — how `lore search` reads free text and ranks its hits:
// logic/searchrank plans and scores, opsmemory.Match runs the any-word
// search and each word's own search (D-435, D-436). Every `<kind> search`
// goes through the same two (opsmemory.SearchFields, D-461), so a question
// finds the same rows in the same order through either.

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/model/errs"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// scoreInOrder scores hits that matched every term (condition searches:
// meta key:value, resource field:value), keeping their order as rank.
func scoreInOrder(hits []opssearch.Hit, terms []string) {
	for i := range hits {
		hits[i].Score = searchrank.Score(len(terms), len(terms), searchrank.Names(hits[i].Title, terms), searchrank.TitleShare(hits[i].Title, terms), i)
	}
}

// resourceKindNames maps every kind id this lens sees to its name, for
// the resource hits' kind (D-436). No kinds view yet is an empty map.
func (a *App) resourceKindNames(ctx context.Context) (map[string]string, error) {
	res, err := a.SQL(ctx, opssql.Request{Query: `SELECT id, name FROM kinds_all`, Timeout: opssql.DefaultTimeout})
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 1 {
			out[r[0].String()] = r[1].String()
		}
	}
	return out, nil
}
