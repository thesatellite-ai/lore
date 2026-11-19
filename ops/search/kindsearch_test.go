package search_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
)

// kindHitIDs runs a kind's own search (decision.search, …) and returns
// its hit ids in order.
func kindHitIDs(t *testing.T, h *opstest.H, op, query string) []string {
	t.Helper()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(op, `{"query":`+strconv.Quote(query)+`,"limit":1000}`))), &out); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(out.Data))
	for i, d := range out.Data {
		ids[i] = d.ID
	}
	return ids
}

// TestKindSearchesMatchLikeSearch (devco D-461): every kind's own search
// (lore decision search, rule search, …) finds and orders rows as lore
// search does: for each question of the relevance set, the kind's best
// row in lore search is the first hit of the kind's own search. Before,
// those searches needed every word (stop words too), so "how do we
// deploy orbit" found no decision.
func TestKindSearchesMatchLikeSearch(t *testing.T) {
	h := opstest.New(t)
	_, byID := seedRelevance(t, h)
	ops := map[string]string{} // table -> its kind's search op
	for _, tg := range search.Targets() {
		name := tg.Entity + ".search"
		if _, ok := h.App.Registry().Lookup(name); ok {
			ops[tg.Table.String()] = name
		}
	}
	tables := map[string]bool{}
	for _, r := range relevanceRows {
		tables[r.table.String()] = true
	}
	for table := range tables {
		if ops[table] == "" {
			t.Fatalf("no kind search for table %s", table)
		}
	}
	for _, c := range relevanceCases {
		best := map[string]string{} // table -> lore search's first hit of it
		for _, x := range searchHits(t, h, c.query) {
			if _, seen := best[x.EntityTable]; !seen {
				best[x.EntityTable] = x.ID
			}
		}
		for table, want := range best {
			got := kindHitIDs(t, h, ops[table], c.query)
			if len(got) == 0 {
				t.Errorf("%s %q: no hits; lore search finds %s", ops[table], c.query, byID[want])
				continue
			}
			if got[0] != want {
				t.Errorf("%s %q: first %s, lore search's best of the kind is %s", ops[table], c.query, byID[got[0]], byID[want])
			}
		}
	}
}

// TestPrefixQueryTitleShare (devco D-461, D-468): a prefix query (invoi*) counts
// a title word starting with the prefix, so the row titled about invoices
// comes before a row whose body only repeats the word, in lore search and
// in the kind's own search. Before, the prefix term matched no title word
// (title share 0) and bm25 alone put the repeating body first.
func TestPrefixQueryTitleShare(t *testing.T) {
	h := opstest.New(t)
	body := h.Seed(schema.TableDecisions, map[string]string{"title": strconv.Quote("Quarterly report"),
		"body": strconv.Quote("Attach the invoices: invoices, invoicing, invoice totals and every invoice number.")})
	titled := h.Seed(schema.TableDecisions, map[string]string{"title": strconv.Quote("Invoice numbering"),
		"body": strconv.Quote("Numbers are sequential per year.")})
	hits := searchHits(t, h, "invoi*")
	if len(hits) != 2 || hits[0].ID != titled || hits[1].ID != body {
		t.Errorf("lore search invoi*: %+v, want the titled row %s first", hits, titled)
	}
	if got := kindHitIDs(t, h, "decision.search", "invoi*"); len(got) != 2 || got[0] != titled {
		t.Errorf("decision search invoi*: %v, want the titled row %s first", got, titled)
	}
}
