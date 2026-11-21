package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestMatchCountingRunsWordSearchesBare (devco:D-808): a natural query's
// per-word searches run without snippets when the any-word search was
// not cut, and the hits and counts equal Match's; a word search that
// finds a row the any-word search missed, or an any-word search cut at
// the limit, runs with snippets as before.
func TestMatchCountingRunsWordSearchesBare(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rowIDs []ids.AnyID
	for range 4 {
		id, err := ids.New[ids.Memory]()
		if err != nil {
			t.Fatal(err)
		}
		rowIDs = append(rowIDs, id.Any())
	}
	// row i holds the words of text[i]; "extra" is a word the any-word
	// search does not see (as row_expanded text can be)
	text := []string{"orbit status", "orbit", "status", "status extra"}
	w := searchrank.Of("what is the orbit status")
	if !w.Natural || len(w.Terms) != 2 {
		t.Fatalf("plan %+v, want a natural query of two words", w)
	}
	holds := func(q string, i int) bool {
		for _, word := range strings.Fields(text[i]) {
			if strings.Contains(q, word) {
				return true
			}
		}
		return false
	}
	var snippetRuns, bareRuns int
	search := func(snip bool) MatchFunc {
		return func(_ context.Context, q string, limit int) ([]Hit, error) {
			if snip {
				snippetRuns++
			} else {
				bareRuns++
			}
			var out []Hit
			for i, id := range rowIDs {
				if q == w.FTS && i == 3 {
					continue // the any-word search does not find row 3
				}
				if holds(q, i) && len(out) < limit {
					h := Hit{ID: id}
					if snip {
						h.Snippet = "<b>" + text[i] + "</b>"
					}
					out = append(out, h)
				}
			}
			return out, nil
		}
	}
	wantHits, wantCounts, _, err := Match(ctx, search(true), w, 100)
	if err != nil {
		t.Fatal(err)
	}
	snippetRuns, bareRuns = 0, 0
	hits, counts, _, err := MatchCounting(ctx, search(true), search(false), w, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hits, wantHits) || !maps.Equal(counts, wantCounts) {
		t.Errorf("MatchCounting = %v %v, want Match's %v %v", hits, counts, wantHits, wantCounts)
	}
	// any-word search + "status" again with snippets (it finds row 3);
	// "orbit" stays bare
	if snippetRuns != 2 || bareRuns != 2 {
		t.Errorf("snippet searches %d, bare %d; want 2 and 2 (only the word finding a new row runs again with snippets)", snippetRuns, bareRuns)
	}
	snippetRuns, bareRuns = 0, 0
	if _, _, _, err := MatchCounting(ctx, search(true), search(false), w, 2); err != nil {
		t.Fatal(err)
	}
	if bareRuns != 0 {
		t.Errorf("any-word search cut at the limit: %d bare word search(es), want 0", bareRuns)
	}
}
