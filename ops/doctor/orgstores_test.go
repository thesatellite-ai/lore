package doctor

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestOrgStoreLineNamesEveryStore (devco D-727): doctor's line for the
// org's store names every store of the person's org entry with who uses
// it, so a team member sees the team's store beside the main one; an entry
// with only the main store keeps its line as before.
func TestOrgStoreLineNamesEveryStore(t *testing.T) {
	t.Parallel()
	org := targetStore + "acme"
	rs := []onboard.CheckResult{{Check: onboard.CheckStoreRead, Status: onboard.StatusOK, Target: org}}
	main := orgconf.Store{URL: "git@github.com:acme-co/lore-team.git", OrgFolder: "org"}
	team := orgconf.Store{URL: "git@github.com:acme-co/lore-secret.git", Audience: orgconf.TeamAudience("@secret")}
	ops := orgconf.Store{URL: "git@github.com:acme-co/lore-ops.git", Audience: orgconf.AudienceAdmins}
	two := SetupReport{Name: "acme-member", Org: "acme", summary: setupSummary{orgStores: []orgconf.Store{main, team, ops}}}
	got := areaLine(org, rs, two)
	for _, want := range []string{"lore-team (org folder)", "lore-secret (team:@secret)", "lore-ops (admins)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("org store line lacks %q: %q", want, got)
		}
	}
	if got := areaLine(targetStore+storeMe, rs, two); strings.Contains(got, "lore-secret") {
		t.Fatalf("the store me line names the org's stores: %q", got)
	}
	one := SetupReport{Name: "acme-member", Org: "acme", summary: setupSummary{orgStores: []orgconf.Store{main}}}
	if got := areaLine(org, rs, one); strings.Contains(got, "stores") {
		t.Fatalf("a one-store entry's line changed: %q", got)
	}
}
