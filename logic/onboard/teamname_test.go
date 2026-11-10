package onboard

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/row"
)

// The team step's name rule is what the save takes: the name becomes an
// orgs[] entry (orgconf.ValidateOrgName) and the team store's name in
// .lore/stores.yaml (row.ValidName). Before, the step asked for
// snake_case, so "acme_hub" passed the step and the save then refused it
// (E_CONFIG_INVALID), and "acme-hub" passes orgs[] but not stores.yaml.
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
	for _, name := range []string{"acme-hub", "agency", "acme2", "acme.io", "acme_hub", "Acme", "-acme", "life_team", "2acme"} {
		stepOK := true
		for _, r := range team.Rules {
			if _, ok := ValidateValue(r, name); !ok {
				stepOK = false
			}
		}
		saveOK := orgconf.ValidateOrgName(name) == nil && row.ValidName(name)
		if stepOK != saveOK {
			t.Errorf("team name %q: the step says ok=%v, config.yaml says ok=%v", name, stepOK, saveOK)
		}
	}
}
