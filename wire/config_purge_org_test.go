package wire

import (
	"strings"
	"testing"
	"time"
)

// purgeOrgConfig is an admin setup of team hub beside a solo one; the team
// entry is only the admin setup's.
const purgeOrgConfig = `orgs:
  - name: hub
    stores:
      - url: git@github.com:octo-personal/lore-team.git
        org_folder: org
setups:
  - name: personal
    kind: solo
  - name: hubadmin
    kind: admin
    org: hub
`

// TestPurgeAdminFreesItsTeam (owner decision 2026-11-08): removing an
// admin setup kept its orgs[] entry for good, so re-adding it was refused
// by org.name_free with no way out but a hand edit. Purging it now frees
// the team, unless another setup (live or trashed) still names it.
func TestPurgeAdminFreesItsTeam(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

	home := writeSetupsConfig(t, purgeOrgConfig)
	if found, err := TrashSetup(home, "hubadmin", at); err != nil || !found {
		t.Fatalf("trash: %v %v", found, err)
	}
	if got := readSetupsFile(t, home); !strings.Contains(got, "name: hub\n") {
		t.Fatalf("the trash dropped the team while the setup can be restored:\n%s", got)
	}
	res, err := PurgeSetup(home, "hubadmin", PurgeOptions{At: at})
	if err != nil || !res.OrgCleared {
		t.Fatalf("purge: %+v %v, want the team entry cleared", res, err)
	}
	if got := readSetupsFile(t, home); strings.Contains(got, "orgs:") || strings.Contains(got, "name: hub\n") {
		t.Fatalf("the purged admin setup's team is still in config.yaml:\n%s", got)
	}

	// A member setup of the same team still names it: the entry stays.
	shared := writeSetupsConfig(t, purgeOrgConfig+"  - name: hubwork\n    kind: member\n    org: hub\n")
	if _, err := TrashSetup(shared, "hubadmin", at); err != nil {
		t.Fatal(err)
	}
	if res, err := PurgeSetup(shared, "hubadmin", PurgeOptions{At: at}); err != nil || res.OrgCleared {
		t.Fatalf("purge with another setup of the team: %+v %v, want the team kept", res, err)
	}
	if got := readSetupsFile(t, shared); !strings.Contains(got, "name: hub\n") {
		t.Fatalf("the team another setup uses was removed:\n%s", got)
	}
}
