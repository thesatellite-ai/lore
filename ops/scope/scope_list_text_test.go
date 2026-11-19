package scope_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/textwidth"
	"github.com/thesatellite-ai/lore/ops/scope"
)

// TestScopeListCountsEveryRuleAndAlignsCJK is devco:Q-14: a folder with a
// should and a must rule shows 2 rules, a should-only folder 1, and a CJK
// folder name does not shift the columns after it.
func TestScopeListCountsEveryRuleAndAlignsCJK(t *testing.T) {
	h := scopeHarness(t, "docs", "src/Charts", "src/çizelge-日本語")
	h.Must("rule.add", `{"body":"a","severity":"should","scope":"dir:docs"}`)
	h.Must("rule.add", `{"body":"b","severity":"must","scope":"dir:docs"}`)
	h.Must("rule.add", `{"body":"c","severity":"should","scope":"dir:src/Charts"}`)
	h.Must("rule.add", `{"body":"d","severity":"should","scope":"dir:src/çizelge-日本語"}`)
	var list scope.ListOut
	decode(t, h, "scope.list", `{}`, &list)
	got := map[string]int{}
	for _, r := range list.Data {
		got[r.Path] = r.Rules
	}
	if got["docs"] != 2 || got["src/Charts"] != 1 || got["src/çizelge-日本語"] != 1 {
		t.Fatalf("rules per folder %v, want docs 2, src/Charts 1, src/çizelge-日本語 1", got)
	}
	lines := strings.Split(strings.TrimRight(list.Text(), "\n"), "\n")
	col := -1
	for _, l := range lines {
		i := strings.Index(l, "(none)")
		if i < 0 {
			i = strings.Index(l, "file")
		}
		w := textwidth.Width(l[:i])
		if col >= 0 && w != col {
			t.Fatalf("file column starts at cell %d, want %d:\n%s", w, col, list.Text())
		}
		col = w
	}
}
