package wire

import (
	"slices"
	"testing"
)

// TestActiveTeamHandlesForAMiss (devco:J-14): a handle miss names the
// active teams (a renamed team's new handle is among them), sorted, never
// a removed one, and at most maxTeamsNamed.
func TestActiveTeamHandlesForAMiss(t *testing.T) {
	t.Parallel()
	got := activeTeamHandles([]RosterTeam{{Handle: "web", Active: false}, {Handle: "payments", Active: true}, {Handle: "infra", Active: true}})
	if !slices.Equal(got, []string{"@infra", "@payments"}) {
		t.Fatalf("got %v", got)
	}
	var many []RosterTeam
	for _, h := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		many = append(many, RosterTeam{Handle: h, Active: true})
	}
	if got := activeTeamHandles(many); len(got) != maxTeamsNamed+1 || got[maxTeamsNamed] != "…" {
		t.Fatalf("many: %v", got)
	}
}
