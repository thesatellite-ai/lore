package renderhost

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestPrivateBudgetLineStatesTheRealLimit (devco D-430, D-909): a private
// file whose pinned rules and hotfixes alone are over context.budget.internal
// used to say so with both sizes on a file larger than its budget; the
// budget is hard now, so the file stays within it and its closing lines
// name the budget and the commands that print the rest.
func TestPrivateBudgetLineStatesTheRealLimit(t *testing.T) {
	over := fitPrivate([]privateBody{privBody("H", 40, 4)}, 1000, 0)
	txt := over.texts[0]
	if len(txt) > 1000 || !strings.Contains(txt, "not listed to stay within context.budget.internal 1000 bytes") || strings.Contains(txt, "never cut") {
		t.Fatalf("over-core file (%d bytes) does not state the real limit:\n%s", len(txt), txt)
	}
	under := fitPrivate([]privateBody{privBody("C", 0, 30)}, 600, 0)
	if txt := under.texts[0]; !strings.Contains(txt, "left out by context.budget.internal 600 bytes") || strings.Contains(txt, "never cut") {
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
