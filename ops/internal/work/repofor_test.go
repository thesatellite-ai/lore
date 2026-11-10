package work

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// repoEnv answers RepoID only (the repo store's repos rows); groupEnv adds
// GroupRepoID. Every other Env method is the nil interface's (unused).
type repoEnv struct {
	Env
	calls *[]string
}

func (e repoEnv) RepoID(context.Context, string) (ids.ID[ids.Repo], error) {
	*e.calls = append(*e.calls, "repo")
	return ids.Parse[ids.Repo]("rep_01a1a5aca7f57659be7320c5f4b700a1")
}

type groupEnv struct{ repoEnv }

func (e groupEnv) GroupRepoID(_ context.Context, to store.Source, _ string) (ids.AnyID, error) {
	*e.calls = append(*e.calls, "group:"+string(to))
	return ids.ParseAny("rep_01a1a5aca7f57659be7320c5f4b700a2")
}

// TestRepoFor: the repo store (to "" or repo) always uses RepoID; another
// target uses GroupRepoID when the Env has it, else falls back to RepoID.
func TestRepoFor(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		group bool
		to    store.Source
		want  string
	}{
		{false, "", "repo"}, {false, store.SourceRepo, "repo"}, {false, "agency", "repo"},
		{true, "", "repo"}, {true, store.SourceRepo, "repo"}, {true, "agency/org", "group:agency/org"},
	} {
		var calls []string
		var env Env = repoEnv{calls: &calls}
		if c.group {
			env = groupEnv{repoEnv{calls: &calls}}
		}
		if _, err := RepoFor(ctx, env, c.to, "backend"); err != nil || len(calls) != 1 || calls[0] != c.want {
			t.Errorf("group=%v to=%q: calls %v err %v, want %s", c.group, c.to, calls, err, c.want)
		}
	}
}
