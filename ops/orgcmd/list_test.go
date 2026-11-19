package orgcmd

import (
	"strings"
	"testing"
)

// TestOrgListPrintsEntriesStoresSetups guards devco D-570: `lore org list`
// was an unknown command; it prints each org entry with its stores (the
// org folder one and an admins-only one marked), the setups that use it
// with their role, and a count line.
func TestOrgListPrintsEntriesStoresSetups(t *testing.T) {
	out := ListOut{Orgs: []ListOrg{{Name: "acme", ID: "org_1", Key: "human:ada@acme.test",
		Stores: []ListStore{{Name: "lore-team", URL: "git@github.com:acme-co/lore-team.git", OrgFolder: "org"},
			{Name: "lore-ops", URL: "git@github.com:acme-co/lore-ops.git", Audience: "admins"}},
		Setups: []ListSetup{{Name: "acme-hub", Role: "admin"}}}}}.Text()
	for _, want := range []string{
		"acme  org_1\n",
		"  you      human:ada@acme.test\n",
		"  setups   acme-hub (admin)\n",
		"  stores   lore-team  git@github.com:acme-co/lore-team.git  org folder org\n",
		"           lore-ops   git@github.com:acme-co/lore-ops.git   admins only\n",
		"1 org entry ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("org list lacks %q:\n%s", want, out)
		}
	}
	if got := (ListOut{}).Text(); !strings.Contains(got, "lore org import --url") {
		t.Errorf("an empty org list names no next step: %q", got)
	}
}
