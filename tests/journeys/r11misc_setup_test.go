//go:build journeys && !windows

package journeys

// r11misc_setup_test.go — devco D-451 and D-447: Lee names the team
// "acme-ops" (a dashed name, like setup names; before, the team step said
// "lowercase letters and digits" and the org had to be "acmeops"), writes
// and reads its company folder by that name, and then joins the team from a
// second laptop answering the kind step "member": the roster already names
// him an admin, so the setup becomes an admin setup, and the join step now
// says so (before, setup list showed KIND admin with no word why).

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyDashedOrgAndAdminJoinSaysUpgrade(t *testing.T) {
	t.Parallel()
	const org = "acme-ops"
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	dir := tt.repo[tlLee.Email].Dir

	w.Step("day1/team-named-with-a-dash", func() {
		tlStartTeam(t, lee, dir, tlLeadSetup, org, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(dir, "rule", "add", "--to", org+"/org", "--body", tlRuleBody, "--severity", "should").OK(t)
		lee.Lore(dir, "sync").OK(t)
		// read from a folder that is no repo (an un-linked repo reads its own
		// .lore only, whatever the org's name)
		code := filepath.Join(lee.Home, "code")
		lee.Lore(code, "rule", "list", "--store", org+"/org").OK(t).Says(t, tlRuleBody, "["+org+"/org]")
		lee.Lore(code, "setup", "list").OK(t).Says(t, org)
	})

	w.Step("day2/second-laptop-joins-as-member", func() {
		w.AdvanceDays(1)
		desk := w.personas[0].NewLaptop("desk")
		tlGitHub(t, w, desk, tlLee.Login)
		home := desk.Home
		tlFirstSteps(t, desk, home, tlTeamSetup, "member", tlLee)
		r := tlApply(t, desk, home, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		if !strings.Contains(r.Out(), "saved as an admin setup, not member") || !strings.Contains(r.Out(), `roster role in \"`+org+`\" is admin`) {
			t.Errorf("the join made the member setup an admin setup without saying why:\n%s", r)
		}
		desk.Lore(home, "setup", "list").OK(t).Says(t, "admin")
	})
}
