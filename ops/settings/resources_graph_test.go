package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceGraphImpact: graph --down / --up over every ref field, a
// node reached twice prints (seen above), a cycle prints (cycle), and
// impact lists dependents by hops (RESOURCES 3.21).
func TestResourceGraphImpact(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"parent","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"database"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"database","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"uses","type":"ref","ref_kind":"database"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub_prod"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"database","name":"hub_pg","set":["runs_on=hub_prod"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"hub_api","set":["runs_on=hub_prod","uses=hub_pg"]}`)
	down := opstest.Text(h.Must(settings.OpResGraph, `{"id":"hub_api"}`))
	if !strings.Contains(down, "runs_on  host hub_prod") || !strings.Contains(down, "(seen above)") {
		t.Fatalf("down:\n%s", down)
	}
	up := opstest.Text(h.Must(settings.OpResGraph, `{"id":"hub_prod","up":true}`))
	if !strings.Contains(up, "runs_on ← database hub_pg") || !strings.Contains(up, "uses ← service hub_api") {
		t.Fatalf("up:\n%s", up)
	}
	imp := opstest.Text(h.Must(settings.OpResImpact, `{"id":"hub_prod"}`))
	if !strings.Contains(imp, "what breaks if host hub_prod goes down") || !strings.Contains(imp, "1  database") {
		t.Fatalf("impact:\n%s", imp)
	}
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"loop_a"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"loop_b","set":["parent=loop_a"]}`)
	h.MustYes(settings.OpResEdit, `{"id":"loop_a","set":["parent=loop_b"]}`)
	if c := opstest.Text(h.Must(settings.OpResGraph, `{"id":"loop_a"}`)); !strings.Contains(c, "parent  host loop_a   (cycle)") {
		t.Fatalf("cycle:\n%s", c)
	}
}
