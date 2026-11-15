package wire_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestEveryAddKeepsSourceRef (devco D-425): every add whose row keeps a
// source_ref offers the one shared --source-ref flag (json source_ref) and
// stores it, so `<noun> show` gives it back. hotfix, pattern, playbook and
// snapshot add had no such flag: agents put their sources into the body.
func TestEveryAddKeepsSourceRef(t *testing.T) {
	h := opstest.New(t)
	const ref = "https://tickets.example/T-9"
	adds := map[string]string{
		"memory":   `{"body":"the build needs go 1.22"`,
		"rule":     `{"body":"use go 1.22"`,
		"decision": `{"title":"go 1.22","body":"we pin go 1.22"`,
		"hotfix":   `{"title":"retry the push","body":"the remote drops the first push"`,
		"pattern":  `{"title":"wrap errors","body":"fmt.Errorf with %w"`,
		"playbook": `{"title":"release","body":"tag, build, upload"`,
		"snapshot": `{"title":"week 1","body":"what we know"`,
	}
	for noun, in := range adds {
		e, ok := h.App.Registry().Lookup(noun + ".add")
		if !ok {
			t.Fatalf("no op %s.add", noun)
		}
		found := false
		for _, f := range e.InputFields() {
			if f.Name == "source_ref" {
				found = f.Flag == "source-ref"
			}
		}
		if !found {
			t.Errorf("%s add has no --source-ref (json source_ref)", noun)
			continue
		}
		id := h.ID(h.MustYes(noun+".add", in+`,"source_ref":"`+ref+`"}`))
		if got := h.JSON(h.Must(noun+".show", `{"id":"`+id+`"}`)); !strings.Contains(got, `"source_ref":"`+ref+`"`) {
			t.Errorf("%s show does not give back the source_ref:\n%s", noun, got)
		}
	}
}
