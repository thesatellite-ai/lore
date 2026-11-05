package actorproject

import (
	"maps"
	"testing"
)

// TestClientProjection: kind × display × agent_details; only kind and
// display_name ever come out, and a row that already matches needs no patch.
func TestClientProjection(t *testing.T) {
	cases := []struct {
		name        string
		kind, disp  string
		agentClient bool
		ok          bool
	}{
		{"person", "human", "Bob Roe", false, true},
		{"person, agent_details client", "human", "Bob Roe", true, true},
		{"system actor", "system", "lore", false, true},
		{"agent, agent_details internal", KindAgent, "bob via codex", false, false},
		{"agent, agent_details client", KindAgent, "bob via codex", true, true},
		{"no display name", "human", "", true, false},
		{"agent without display name", KindAgent, "", true, false},
		{"unicode name", "human", "Zoë Ångström", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ok := ClientProjection(c.kind, c.disp, c.agentClient)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				if p != (ClientActor{}) {
					t.Fatalf("a refused projection carries %+v", p)
				}
				return
			}
			want := map[string]string{FieldKind: c.kind, FieldDisplayName: c.disp}
			if got := p.Fields(); !maps.Equal(got, want) {
				t.Fatalf("Fields = %v, want %v", got, want)
			}
			same := func(f string) string { return want[f] }
			if patch := p.Patch(same); len(patch) != 0 {
				t.Fatalf("a matching row patched %v", patch)
			}
			renamed := func(f string) string {
				if f == FieldDisplayName {
					return "old name"
				}
				return want[f]
			}
			if patch := p.Patch(renamed); !maps.Equal(patch, map[string]string{FieldDisplayName: c.disp}) {
				t.Fatalf("rename patch = %v", patch)
			}
			empty := func(string) string { return "" }
			if patch := p.Patch(empty); !maps.Equal(patch, want) {
				t.Fatalf("patch over an empty row = %v", patch)
			}
		})
	}
	if got := (ClientActor{DisplayName: "x"}).Fields(); !maps.Equal(got, map[string]string{FieldDisplayName: "x"}) {
		t.Fatalf("an empty kind is left out: %v", got)
	}
}
