package renderhost

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestPrivateBudgetLineStatesTheRealLimit (devco D-430): a private file
// whose pinned rules and hotfixes alone are over context.budget.internal
// says so with both sizes, instead of "left out by … 8000 bytes" on a
// 21 KB file; under the budget the line is unchanged.
func TestPrivateBudgetLineStatesTheRealLimit(t *testing.T) {
	over := fitPrivate([]privateBody{privBody(strings.Repeat("x", 300), 2, 4)}, 100, budgetFix{})
	if txt := over.texts[0]; !strings.Contains(txt, "context.budget.internal is 100 bytes") || !strings.Contains(txt, "already take 300 bytes (never cut") {
		t.Fatalf("over-core file does not state the real limit:\n%s", txt)
	}
	under := fitPrivate([]privateBody{privBody("C", 0, 30)}, 200, budgetFix{})
	if txt := under.texts[0]; !strings.Contains(txt, "left out by context.budget.internal 200 bytes") || strings.Contains(txt, "never cut") {
		t.Fatalf("under-budget file line changed:\n%s", txt)
	}
}

// TestPrivateRenderDropsNoise (devco D-430): a rule with no or the
// default trust_score gets no trust="0.50" attribute (another value still
// shows), and a body line that only points at a lore row file of another
// repo is not rendered.
func TestPrivateRenderDropsNoise(t *testing.T) {
	text := func(s string) value.Value {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if a := privateTrustAttr(row.Row{Fields: map[string]value.Value{}}); a != "" {
		t.Fatalf("unset trust rendered as %q", a)
	}
	if a := privateTrustAttr(row.Row{Fields: map[string]value.Value{renderFieldTrustScore: text("0.5")}}); a != "" {
		t.Fatalf("default trust rendered as %q", a)
	}
	if a := privateTrustAttr(row.Row{Fields: map[string]value.Value{renderFieldTrustScore: text("0.8")}}); a != ` trust="0.80"` {
		t.Fatalf("set trust rendered as %q", a)
	}
	body := "the worker does not hot-reload\nSource: app/.lore/data/hotfixes/hfx_01.json\nrestart it\n  Source: other/.lore/data/rules/rul_02.json"
	got := renderField(row.Row{Fields: map[string]value.Value{renderFieldBody: text(body)}}, renderFieldBody)
	if strings.Contains(got, "Source:") || !strings.Contains(got, "the worker does not hot-reload\nrestart it") {
		t.Fatalf("body rendered as %q", got)
	}
	keep := "Source: the vendor's runbook, page 4"
	if got := renderField(row.Row{Fields: map[string]value.Value{renderFieldBody: text(keep)}}, renderFieldBody); got != keep {
		t.Fatalf("a plain Source line was dropped: %q", got)
	}
}
