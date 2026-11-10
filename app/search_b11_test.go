package app

// B11 port of saas/pkg/aicoder/fts5 query_test.go (TestQuoteTerms,
// TestSearch_PunctuatedFreeTextAndFTSSyntax) through the release binary's
// whole path: free text with FTS5 punctuation finds the row literally,
// while FTS5 prefix and boolean syntax is still honoured.

import (
	"encoding/json"
	"testing"
)

// TestSearchPunctuatedFreeTextAndFTSSyntax: "round-trip" (a '-' FTS5 would
// read as a column filter) and "backup:" (column syntax) match literally;
// "round*" and "plain OR trip" keep their FTS5 meaning; a word in no row
// matches nothing; none of them is an error.
func TestSearchPunctuatedFreeTextAndFTSSyntax(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	root := w.initRepo(t)
	for _, body := range []string{"round-trip through backup", "plain roundabout text"} {
		if code, _, errOut := w.lore(t, root, "memory", "add", "--body", body); code != 0 {
			t.Fatalf("memory add %q: exit %d %s", body, code, errOut)
		}
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"round-trip", 1},
		{"backup:", 1},
		{"round*", 2},
		{"plain OR trip", 2},
		{"nothing-matches", 0},
	} {
		code, out, errOut := w.lore(t, root, "search", tc.query, "--json")
		if code != 0 {
			t.Fatalf("search %q: exit %d %s", tc.query, code, errOut)
		}
		if got := searchHitCount(t, out); got != tc.want {
			t.Errorf("search %q: %d hits, want %d\n%s", tc.query, got, tc.want, out)
		}
	}
}

// searchHitCount is data.count of `lore search --json` (the search.global
// envelope), checked against the length of data.hits.
func searchHitCount(t *testing.T, out string) int {
	t.Helper()
	var env struct {
		Data struct {
			Count int               `json:"count"`
			Hits  []json.RawMessage `json:"hits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("search --json is not the search envelope: %v\n%s", err, out)
	}
	if env.Data.Count != len(env.Data.Hits) {
		t.Fatalf("search --json count %d, %d hits\n%s", env.Data.Count, len(env.Data.Hits), out)
	}
	return env.Data.Count
}
