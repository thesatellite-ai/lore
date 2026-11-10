package settings_test

import (
	"encoding/json"
	"fmt"
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

// graphOut is the --json shape of resource.graph the via test reads.
type graphOut struct {
	Nodes []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Hops int    `json:"hops"`
		Via  []struct {
			Field string `json:"field"`
			From  string `json:"from"`
			To    string `json:"to"`
		} `json:"via"`
	} `json:"nodes"`
}

// TestResourceGraphViaDirection (issue 22a): via[].from names the node the
// walk came FROM in both directions, the deprecated via[].to still holds
// the same id, and the text tree reads in the walk's direction, for
// --down / --up × depth 1..3 × text / json, over the chain
// lb1 --backend--> sa --runs_on--> g1.
func TestResourceGraphViaDirection(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"lb"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"lb","field":"backend","type":"ref","ref_kind":"service"}`)
	ids := map[string]string{}
	ids["g1"] = h.ID(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"g1"}`))
	ids["sa"] = h.ID(h.MustYes(settings.OpResAdd, `{"kind":"service","name":"sa","set":["runs_on=g1"]}`))
	ids["lb1"] = h.ID(h.MustYes(settings.OpResAdd, `{"kind":"lb","name":"lb1","set":["backend=sa"]}`))
	// hop is one node the walk reaches: its name, the field, the node it
	// came from, and the text line that shows the edge.
	type hop struct{ name, field, from, line string }
	walks := []struct {
		dir  string
		root string
		hops []hop
	}{
		{"down", "lb1", []hop{{"sa", "backend", "lb1", "└── backend  service sa"}, {"g1", "runs_on", "sa", "    └── runs_on  host g1"}}},
		{"up", "g1", []hop{{"sa", "runs_on", "g1", "└── runs_on ← service sa"}, {"lb1", "backend", "sa", "    └── backend ← lb lb1"}}},
	}
	for _, w := range walks {
		for depth := 1; depth <= 3; depth++ {
			in := fmt.Sprintf(`{"id":%q,%q:true,"depth":%d}`, w.root, w.dir, depth)
			want := w.hops[:min(depth, len(w.hops))]
			t.Run(fmt.Sprintf("%s/depth%d/json", w.dir, depth), func(t *testing.T) {
				var got graphOut
				if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResGraph, in))), &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Nodes) != len(want) {
					t.Fatalf("nodes = %+v, want %d", got.Nodes, len(want))
				}
				for i, wh := range want {
					n := got.Nodes[i]
					if n.Name != wh.name || n.Hops != i+1 || len(n.Via) != 1 {
						t.Fatalf("node %d = %+v, want %s at hop %d with one via", i, n, wh.name, i+1)
					}
					v := n.Via[0]
					if v.Field != wh.field || v.From != ids[wh.from] || v.To != ids[wh.from] {
						t.Errorf("node %s via = %+v, want field %s from %s (%s), to the same", wh.name, v, wh.field, wh.from, ids[wh.from])
					}
				}
			})
			t.Run(fmt.Sprintf("%s/depth%d/text", w.dir, depth), func(t *testing.T) {
				s := opstest.Text(h.Must(settings.OpResGraph, in))
				lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
				if len(lines) != 1+len(want) || !strings.HasSuffix(lines[0], " "+w.root) {
					t.Fatalf("tree:\n%s", s)
				}
				for i, wh := range want {
					if lines[i+1] != wh.line {
						t.Errorf("line %d = %q, want %q\ntree:\n%s", i+1, lines[i+1], wh.line, s)
					}
				}
			})
		}
	}
}
