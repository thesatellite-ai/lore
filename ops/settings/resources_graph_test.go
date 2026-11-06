package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
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

// TestResourceGraphConflictedKind (RES 3.13): a kind whose row holds
// conflict markers is walked with its last good definition and a note
// naming it; on a machine that never saw it good it is skipped with a
// note, and the rest of the graph still answers.
func TestResourceGraphConflictedKind(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub_prod"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"hub_api","set":["runs_on=hub_prod"]}`)
	h.Must(settings.OpResGraph, `{"id":"hub_prod","up":true}`)
	kid := kindID(t, h, "service")
	markRow(t, h, schema.TableKinds, kid)
	warm := h.Reopen()
	up := opstest.Text(warm.Must(settings.OpResGraph, `{"id":"hub_prod","up":true}`))
	if !strings.Contains(up, "runs_on ← service hub_api") || !strings.Contains(up, "showing its last good definition") || !strings.Contains(up, kid) {
		t.Errorf("warm graph:\n%s", up)
	}
	cold := h.ReopenCold()
	res := cold.Must(settings.OpResGraph, `{"id":"hub_prod","up":true}`)
	s := opstest.Text(res)
	if !strings.Contains(s, "skipped kind "+kid) || strings.Contains(s, "runs_on ←") || strings.Contains(s, "<<<<<<<") {
		t.Errorf("cold graph:\n%s", s)
	}
	if imp := opstest.Text(cold.Must(settings.OpResImpact, `{"id":"hub_prod"}`)); !strings.Contains(imp, "skipped kind "+kid) {
		t.Errorf("cold impact:\n%s", imp)
	}
}
