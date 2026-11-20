package search_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
)

// TestGlobalSearchSaysWhenCut (devco D-767, the personal knowledge base
// benchmark): fifteen notes of a personal store match. The default search
// lists ten and said "Found 10 hits", read as the whole answer, and
// --limit 50 still listed ten (the per-kind cut stayed 10). A cut list
// now leads "Showing the best", ends with a note naming --limit and says
// "more": true in --json; --limit alone lifts the per-kind cut, and a
// list holding every match says nothing more.
func TestGlobalSearchSaysWhenCut(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	for i := range 15 {
		h.SeedTo("me", schema.TableMemories, map[string]string{"body": fmt.Sprintf(`"%s budget note %d"`, storeSearchWord, i)})
	}
	run := func(in string) search.RunOut {
		t.Helper()
		var out search.RunOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, in))), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	q := `"query":"` + storeSearchWord + `"`
	cases := []struct {
		name, in string
		hits     int
		more     bool
	}{
		{"default cuts at the per-kind 10", `{` + q + `}`, 10, true},
		{"--limit alone lists every match", `{` + q + `,"limit":50}`, 15, false},
		{"--limit exactly the matches", `{` + q + `,"limit":15}`, 15, false},
		{"--limit below the matches", `{` + q + `,"limit":14}`, 14, true},
		{"--per-table below --limit", `{` + q + `,"limit":50,"per_table":5}`, 5, true},
	}
	for _, c := range cases {
		out := run(c.in)
		if len(out.Data.Hits) != c.hits || out.Data.More != c.more {
			t.Errorf("%s: %d hits more=%v, want %d more=%v", c.name, len(out.Data.Hits), out.Data.More, c.hits, c.more)
		}
		txt := opstest.Text(h.Must(search.OpRun, c.in))
		cut := strings.HasPrefix(txt, "Showing the best ") && strings.Contains(txt, "more rows match than the ") && strings.Contains(txt, "--limit ")
		if c.more != cut || strings.Contains(txt, "Found ") == c.more {
			t.Errorf("%s: the terminal text does not say whether the list is cut (more=%v):\n%s", c.name, c.more, txt)
		}
	}
}
