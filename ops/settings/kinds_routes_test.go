package settings_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// fixedResourceVerbs are the one-segment literal REST paths under
// /v1/resources/ of every registered operation (what a kind's own
// /v1/resources/<kind> route would collide with).
func fixedResourceVerbs(t *testing.T, h *opstest.H) []string {
	t.Helper()
	var out []string
	for _, e := range h.App.Registry().Entries() {
		r, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		seg, isRes := strings.CutPrefix(r.Path, "/v1/resources/")
		if isRes && seg != "" && !strings.ContainsAny(seg, "/{") && !slices.Contains(out, seg) {
			out = append(out, seg)
		}
	}
	return out
}

// TestKindNameRouteCollision (G-META-9): a kind may not take the name of a
// fixed resource command (its REST route /v1/resources/<name> would be the
// command's): kind add and kind rename refuse every such name with
// E_INVALID_INPUT and a hint, writing nothing; the set is the registry's,
// so it includes the verbs g-render saw collide; a near name passes; a kind
// with such a name from an earlier build or a hand edit is a resource lint
// finding naming the rename.
func TestKindNameRouteCollision(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	verbs := fixedResourceVerbs(t, h)
	for _, want := range []string{"adopt", "clone", "export", "import", "kinds", "lint", "rehome", "repoint", "report", "stale"} {
		if !slices.Contains(verbs, want) {
			t.Fatalf("registry walk misses %s: %v", want, verbs)
		}
	}
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	for _, v := range verbs {
		_, err := h.RunYes(settings.OpKindAdd, `{"kind":"`+v+`"}`)
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "pick another name") {
			t.Errorf("kind add %s: %v, want E_INVALID_INPUT with a hint", v, err)
		}
		if _, err = h.RunYes(settings.OpKindRename, `{"id":"host","new_name":"`+v+`"}`); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("kind rename host -> %s: %v, want E_INVALID_INPUT", v, err)
		}
	}
	kinds := opstest.Text(h.Must(settings.OpKinds, `{}`))
	for _, v := range verbs {
		if strings.Contains(kinds, "\t"+v+"\t") {
			t.Errorf("a refused kind %s was written:\n%s", v, kinds)
		}
	}
	h.MustYes(settings.OpKindAdd, `{"kind":"lint_rule"}`)
	h.MustYes(settings.OpKindRename, `{"id":"lint_rule","new_name":"report_item"}`)

	h.Seed("kinds", map[string]string{"name": `"lint"`})
	var out settings.ResLintOut
	if err := json.Unmarshal([]byte(h.JSON(h.MustYes(settings.OpResLint, `{}`))), &out); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range out.Findings {
		if f.Check == "kind_route_collision" {
			n++
			if f.Kind != "lint" || !strings.Contains(f.Detail, "lore resource kind rename lint") {
				t.Errorf("finding %+v", f)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want one kind_route_collision finding, got %d: %+v", n, out.Findings)
	}
}
