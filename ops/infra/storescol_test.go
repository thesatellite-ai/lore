package infra_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/ops/infra"
)

// TestMergedListStores (FB-ACTOR-STORES): a list merged from two stores
// shows a STORES column and a count line; one read from a single store
// keeps its v0.1.17 lines.
func TestMergedListStores(t *testing.T) {
	t.Parallel()
	two := infra.ActorListOut{Data: []infra.Actor{
		{ID: "act_a", Kind: "human", DisplayName: "pat", Stores: []string{"repo", "me"}},
		{ID: "act_b", Kind: "agent", DisplayName: "pat via claude_code", Stores: []string{"me"}},
	}}.Text()
	want := "act_a [human] pat                  repo, me\nact_b [agent] pat via claude_code  me\n2 actors across 2 stores\n"
	if two != want {
		t.Errorf("merged actors:\n got %q\nwant %q", two, want)
	}
	one := infra.ActorListOut{Data: []infra.Actor{{ID: "act_a", Kind: "human", DisplayName: "pat", Stores: []string{"repo"}}}}.Text()
	if one != "act_a [human] pat\n" {
		t.Errorf("one store: %q", one)
	}
	m := infra.MachineListOut{Data: []infra.Machine{
		{ID: "mch_00000000aaaa", Stores: []string{"repo", "me"}},
		{ID: "mch_00000000bbbb", Stores: []string{"me"}},
	}}.Text()
	if !strings.Contains(m, "LAST WRITE  STORES") || !strings.HasSuffix(m, "2 machines across 2 stores\n") || !strings.Contains(m, "repo, me") {
		t.Errorf("merged machines:\n%s", m)
	}
	m1 := infra.MachineListOut{Data: []infra.Machine{{ID: "mch_00000000aaaa", Stores: []string{"repo"}}}}.Text()
	if strings.Contains(m1, "STORES") || strings.Contains(m1, "across") {
		t.Errorf("one-store machines:\n%s", m1)
	}
}
