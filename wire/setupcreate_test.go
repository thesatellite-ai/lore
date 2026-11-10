package wire

// setupcreate_test.go — the default repo of "Create a new private repo"
// with an empty url: the first free lore-me[-N] (realmac item 9).

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

func TestPickCreateRepo(t *testing.T) {
	t.Parallel()
	states := func(m map[string]repoState) func(string) repoState {
		return func(n string) repoState {
			if s, ok := m[n]; ok {
				return s
			}
			return repoFree
		}
	}
	allTaken := map[string]repoState{}
	for _, n := range createRepoNames() {
		allTaken[n] = repoTaken
	}
	for _, c := range []struct {
		name  string
		state map[string]repoState
		want  string
	}{
		{"base free", nil, "lore-me"},
		{"base taken", map[string]repoState{"lore-me": repoTaken}, "lore-me-2"},
		{"base and -2 taken", map[string]repoState{"lore-me": repoTaken, "lore-me-2": repoTaken}, "lore-me-3"},
		{"gh cannot tell: stop there", map[string]repoState{"lore-me": repoUnknown}, "lore-me"},
		{"taken then unknown", map[string]repoState{"lore-me": repoTaken, "lore-me-2": repoUnknown}, "lore-me-2"},
		{"every name taken: the base", allTaken, "lore-me"},
	} {
		if got := pickCreateRepo(states(c.state)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if n := createRepoNames(); len(n) != createRepoTries || n[len(n)-1] != "lore-me-9" {
		t.Errorf("names = %v", n)
	}
}

// withCreateTarget leaves every request alone that is not a store check
// answered create with an empty url, and changes nothing without a
// GitHub user (no gh call is made then).
func TestWithCreateTargetLeavesOthers(t *testing.T) {
	t.Parallel()
	sp := setupProber{}
	create := onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreCreate}}
	withURL := onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreCreate}, onboard.QIDStoreURL: {Text: "git@github.com:mira/x.git"}}
	reqs := []opssetup.ProbeRequest{
		{Check: onboard.Check{ID: onboard.CheckStoreRead}, Answers: withURL},
		{Check: onboard.Check{ID: onboard.CheckGitHubSSHAuth}, Answers: create},
		{Check: onboard.Check{ID: onboard.CheckStoreRead}, Answers: create, Target: "git@github.com:mira/y.git"},
		{Check: onboard.Check{ID: onboard.CheckStoreRead}, Answers: create}, // no GitHub user: nothing proposed
	}
	got := sp.withCreateTarget(context.Background(), reqs)
	for i := range reqs {
		if got[i].Target != reqs[i].Target || got[i].Answers[onboard.QIDStoreURL].Text != reqs[i].Answers[onboard.QIDStoreURL].Text {
			t.Errorf("req %d changed: %+v", i, got[i])
		}
	}
}
