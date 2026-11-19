package onboard

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// The team step's name rule is what the save takes: the name becomes an
// orgs[] entry (orgconf.ValidateOrgName) and the team store's name in
// .lore/stores.yaml (which takes org names). Before, the step asked for
// snake_case, so "acme_hub" passed the step and the save then refused it
// (E_CONFIG_INVALID).
func TestTeamNameRuleIsTheOrgRule(t *testing.T) {
	var team Question
	for _, s := range Catalog() {
		for _, q := range s.Questions {
			if q.ID == QIDOrg {
				team = q
			}
		}
	}
	if team.ID == "" {
		t.Fatal("the admin plan has no team name question")
	}
	// devco D-451: the team name takes a dash like a setup name (setup
	// "acme-ops" held org "acmeops" before), and nothing either orgs[] or
	// setups[] refuses ('_' is no orgs[] character, '.' no setups[] one).
	for name, want := range map[string]bool{
		"acme-hub": true, "agency": true, "acme2": true, "acme_hub": false, "acme-ops-2": true,
		"acme.io": false, "Acme": false, "-acme": false, "2acme": false, "lore-x": true,
	} {
		stepOK := true
		for _, r := range team.Rules {
			if _, ok := ValidateValue(r, name); !ok {
				stepOK = false
			}
		}
		if stepOK != want {
			t.Errorf("team name %q: the step says ok=%v, want %v", name, stepOK, want)
		}
		if stepOK && (orgconf.ValidateOrgName(name) != nil || !ValidSetupName(name)) {
			t.Errorf("team name %q passes the step but not config.yaml's orgs[] or setups[] rule", name)
		}
	}
}
