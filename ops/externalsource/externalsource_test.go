package externalsource_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
)

// TestExternalSource walks add / list / enable / disable against
// v0.1.17 (2026-11-04): "✓ external-source <name> added", disabled by
// default, list lines "[on|off] <name> (<kind>) <id>", enable/disable by
// id "✓ external-source <id> enabled|disabled". v0.1.17 accepted any
// --config text; 0.1.18 refuses one that is not JSON (nothing written).
func TestExternalSource(t *testing.T) {
	h := opstest.New(t)
	if got := opstest.Text(h.Must("external_source.list", "")); got != "(no external sources)\n" {
		t.Errorf("empty list %q", got)
	}
	if got := h.JSON(h.Must("external_source.list", "")); got != `{"schema_version":1,"kind":"external-source.list","data":[]}` {
		t.Errorf("empty json %s", got)
	}
	cases := []struct{ name, kind, config string }{
		{"gh", "github", `{"repo":"x/y"}`},
		{"n2", "notion", ""},
		{"ünï 🚀", "gitlab", `[1,2,{"a":null}]`},
		{"null", "custom kind", `"just a string"`},
	}
	ids := make([]string, len(cases))
	var want strings.Builder
	for i, c := range cases {
		in := `{"name":` + opstest.Quote(c.name) + `,"kind":` + opstest.Quote(c.kind)
		if c.config != "" {
			in += `,"config":` + opstest.Quote(c.config)
		}
		res := h.Must("external_source.add", in+"}")
		ids[i] = h.ID(res)
		if got := opstest.Text(res); got != "✓ external-source "+c.name+" added\n" {
			t.Errorf("add line %q", got)
		}
		want.WriteString("[off] " + c.name + " (" + c.kind + ") " + ids[i] + "\n")
	}
	list := h.Must("external_source.list", "")
	if got := opstest.Text(list); got != want.String() {
		t.Errorf("list\n%q\nwant\n%q", got, want.String())
	}
	doc := h.JSON(list)
	if k := contenttest.Keys(t, doc, "data", "0"); strings.Join(k, " ") != "id created_at updated_at project_id kind name config_json" {
		t.Errorf("record keys %v (v0.1.17: id created_at updated_at project_id kind name config_json)", k)
	}
	if got := contenttest.Str(t, doc, "data", "0", "config_json"); got != cases[0].config {
		t.Errorf("config_json %q", got)
	}
	before := h.JSON(h.Must("external_source.list", ""))
	for _, in := range []string{
		`{"name":"bad","kind":"notion","config":"notjson"}`, // bad config
		`{"kind":"notion"}`,        // no name
		`{"name":"x"}`,             // no kind
		`{"name":"x","kind":"  "}`, // blank kind
	} {
		h.Fails("external_source.add", in, errs.InvalidInput)
	}
	if h.JSON(h.Must("external_source.list", "")) != before {
		t.Error("a refused add wrote a row")
	}
	res := h.Must("external_source.enable", `{"id":"`+ids[0]+`"}`)
	if got := opstest.Text(res); got != "✓ external-source "+ids[0]+" enabled\n" {
		t.Errorf("enable line %q", got)
	}
	if !strings.HasPrefix(opstest.Text(h.Must("external_source.list", "")), "[on] gh (github) ") {
		t.Errorf("list after enable %q", opstest.Text(h.Must("external_source.list", "")))
	}
	if !strings.Contains(h.JSON(h.Must("external_source.list", "")), `"enabled":true`) {
		t.Error("json has no enabled:true")
	}
	if got := opstest.Text(h.Must("external_source.disable", `{"id":"`+ids[0]+`"}`)); got != "✓ external-source "+ids[0]+" disabled\n" {
		t.Errorf("disable line %q", got)
	}
	if !strings.HasPrefix(opstest.Text(h.Must("external_source.list", "")), "[off] gh") {
		t.Error("list after disable")
	}
	// enable twice is fine
	h.Must("external_source.enable", `{"id":"`+ids[1]+`"}`)
	h.Must("external_source.enable", `{"id":"`+ids[1]+`"}`)
	for name, c := range map[string]struct {
		id   string
		code errs.Code
	}{
		"by name":      {"gh", errs.NotFound},
		"unknown":      {"ext_01a1a5aca7f57659be7320c5f4b7ffff", errs.NotFound},
		"wrong prefix": {"pat_01a1a5aca7f57659be7320c5f4b7ffff", errs.NotFound},
	} {
		for _, verb := range []string{"enable", "disable"} {
			_, err := h.Run("external_source."+verb, `{"id":"`+c.id+`"}`)
			contenttest.Fails(t, verb+" "+name, err, c.code)
		}
	}
}
