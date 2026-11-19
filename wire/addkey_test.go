package wire_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestEveryAddTakesKey (devco D-439): rule, decision, hotfix, pattern and
// playbook add take the shared --key (json key), as memory add does: a
// retry with the same key returns the first run's row with existing:true
// and writes nothing; another key adds another row. Every add also stamps
// created_by_actor_id: rule rows were written with it null.
func TestEveryAddTakesKey(t *testing.T) {
	h := opstest.New(t)
	adds := map[string]string{
		"memory":   `{"body":"the build needs go 1.22"`,
		"rule":     `{"body":"use go 1.22"`,
		"decision": `{"title":"go 1.22","body":"we pin go 1.22"`,
		"hotfix":   `{"title":"retry the push","body":"the remote drops the first push"`,
		"pattern":  `{"title":"wrap errors","body":"fmt.Errorf with %w"`,
		"playbook": `{"title":"release","body":"tag, build, upload"`,
	}
	for noun, in := range adds {
		e, ok := h.App.Registry().Lookup(noun + ".add")
		if !ok {
			t.Fatalf("no op %s.add", noun)
		}
		found := false
		for _, f := range e.InputFields() {
			if f.Name == "key" {
				found = f.Flag == "key"
			}
		}
		if !found {
			t.Errorf("%s add has no --key (json key)", noun)
			continue
		}
		first := h.MustYes(noun+".add", in+`,"key":"import-7"}`)
		id := h.ID(first)
		if strings.Contains(h.JSON(first), `"existing":true`) {
			t.Errorf("%s add: the first keyed add says existing:\n%s", noun, h.JSON(first))
		}
		again := h.MustYes(noun+".add", in+`,"key":"import-7"}`)
		if got := h.ID(again); got != id {
			t.Errorf("%s add: a retry with the same --key added %s next to %s", noun, got, id)
		}
		if !strings.Contains(h.JSON(again), `"existing":true`) || !strings.Contains(opstest.Text(again), "already added with this --key") {
			t.Errorf("%s add: the retry does not say it wrote nothing:\n%s\n%s", noun, h.JSON(again), opstest.Text(again))
		}
		if other := h.ID(h.MustYes(noun+".add", in+`,"key":"import-8"}`)); other == id {
			t.Errorf("%s add: another --key returned the same row %s", noun, id)
		}
		if got := h.JSON(h.Must(noun+".show", `{"id":"`+id+`"}`)); !strings.Contains(got, `"created_by_actor_id":"act_`) {
			t.Errorf("%s add wrote no created_by_actor_id:\n%s", noun, got)
		}
	}
}
