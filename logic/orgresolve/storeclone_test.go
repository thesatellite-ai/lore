package orgresolve

// storeclone_test.go — devco D-686: a store's clone resolves to the entry
// that attaches that store, before any own: pattern. The ops store
// github.com/acme-co/lore-ops matched the company's own pattern
// github.com/acme-co/* and whoami in its clone named the company.

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

func storeCloneConfig() *orgconf.Config {
	return &orgconf.Config{Orgs: []orgconf.Org{
		{Name: "acme", Own: []string{"github.com/acme-co/*"}, Stores: []orgconf.Store{{URL: "git@github.com:acme-co/lore-team.git", OrgFolder: "org"}}},
		{Name: "acmeops", Stores: []orgconf.Store{{URL: "git@github.com:acme-co/lore-ops.git", OrgFolder: "org"}}},
	}}
}

func TestResolveStoreCloneNamesTheStoresOrgBeforeAPattern(t *testing.T) {
	cases := []struct {
		name, remote, org string
		step              Step
	}{
		{"ops store clone", "github.com/acme-co/lore-ops", "acmeops", StepStore},
		{"company store clone", "github.com/acme-co/lore-team", "acme", StepStore},
		{"company code repo", "github.com/acme-co/platform", "acme", StepPattern},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Resolve(Inputs{Config: storeCloneConfig(), Remotes: []string{c.remote}})
			if err != nil {
				t.Fatal(err)
			}
			if r.OrgName() != c.org || r.Step != c.step {
				t.Fatalf("%s resolved to %q by %v (%s), want %q by %v", c.remote, r.OrgName(), r.Step, r.MatchedBy, c.org, c.step)
			}
		})
	}
}

// A store two entries attach decides nothing: the patterns do, as before.
func TestResolveStoreAttachedTwiceFallsThrough(t *testing.T) {
	cfg := storeCloneConfig()
	cfg.Orgs[0].Stores = append(cfg.Orgs[0].Stores, orgconf.Store{URL: "git@github.com:acme-co/lore-ops.git", OrgFolder: "org"})
	r, err := Resolve(Inputs{Config: cfg, Remotes: []string{"github.com/acme-co/lore-ops"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Step != StepPattern || r.OrgName() != "acme" {
		t.Fatalf("resolved %q by %v, want acme by the own pattern", r.OrgName(), r.Step)
	}
}
