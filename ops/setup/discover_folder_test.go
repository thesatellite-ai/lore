package setup

import (
	"context"
	"slices"
	"testing"
)

// discoverProber answers DiscoverFacts with facts and records the input.
type discoverProber struct {
	*fakeProber
	facts DiscoverFacts
	in    []DiscoverFactsIn
}

func (p *discoverProber) DiscoverFacts(_ context.Context, in DiscoverFactsIn) (DiscoverFacts, error) {
	p.in = append(p.in, in)
	return p.facts, nil
}

// folderEnv is an Onboard whose prober discovers.
type folderEnv struct {
	fakeEnv
	dp *discoverProber
}

func (e folderEnv) SetupProber(context.Context) (Prober, error) { return e.dp, nil }

// --folder reaches the facts half in the order given.
func TestDiscoverPassesFolders(t *testing.T) {
	base := newFakeEnv()
	f, _ := twoAccounts()
	env := folderEnv{fakeEnv: base, dp: &discoverProber{fakeProber: base.prober, facts: f}}
	if _, err := newEngine(env, fakeCatalog{}).Discover(bg, DiscoverIn{Folders: []string{"~/code", "~/work"}}); err != nil {
		t.Fatal(err)
	}
	if len(env.dp.in) != 1 || !slices.Equal(env.dp.in[0].Folders, []string{"~/code", "~/work"}) {
		t.Fatalf("facts input %+v", env.dp.in)
	}
}
