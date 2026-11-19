package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestMaxClaimsIsATeamPolicy (devco:S-20): the WIP limit comes from the
// team policy agents.max_claims_per_agent (built-in 1); a personal
// config.yaml value may lower it but never raise it, so an agent cannot
// lift its own cap; a policy below 1 counts as 1.
func TestMaxClaimsIsATeamPolicy(t *testing.T) {
	setOf := func(n int64) policy.Set {
		t.Helper()
		s, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyAgentsMaxClaimsPerAgent, Value: value.Int(n)}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	unset, err := policy.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name            string
		set             policy.Set
		read            bool
		config          int
		team, effective int
	}{
		{"built-in", unset, true, 0, 1, 1},
		{"config cannot raise the built-in cap", unset, true, 5, 1, 1},
		{"team raises", setOf(3), true, 0, 3, 3},
		{"config lowers the team cap", setOf(3), true, 2, 3, 2},
		{"config above the team cap is ignored", setOf(2), true, 9, 2, 2},
		{"policy 0 counts as 1", setOf(0), true, 0, 1, 1},
		{"policy unreadable keeps the built-in", setOf(4), false, 3, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			team, eff := maxClaims(tc.set, tc.read, tc.config)
			if team != tc.team || eff != tc.effective {
				t.Fatalf("maxClaims = team %d effective %d, want %d %d", team, eff, tc.team, tc.effective)
			}
		})
	}
}
