//go:build journeys && !windows

package journeys

// join_doctor_healthy_test.go — a member who has just joined is HEALTHY
// (devco D-809). The tools step only recorded the picked tool, so right
// after setup lore doctor said Status: DEGRADED with tools.skill_installed
// "claude_code: not installed" until the person ran lore skill install,
// which setup never asked for. The step now installs the skill; its
// output names the folder it wrote, and the doctor in the member's clone
// says HEALTHY with the tool's skill current.

import (
	"path/filepath"
	"testing"
)

func TestJourneyMemberJoinDoctorHealthy(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	lee := tt.person(tlLee)
	leeDir := tt.repo[tlLee.Email].Dir
	tlStartTeam(t, lee, leeDir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})

	tt.w.Step("lee-is-healthy-after-starting-the-team", func() {
		tt.lore(tlLee, "doctor").OK(t).Says(t, "Status: HEALTHY").Never(t, "DEGRADED", "tools.skill_installed", "not installed")
	})

	_, line := tlInvite(t, lee, leeDir, tlAnn.Email)
	ann := tt.person(tlAnn)
	dir := tt.repo[tlAnn.Email].Dir
	skillDir := filepath.Join(ann.Home, ".claude", "skills", "lore")

	tt.w.Step("ann-joins-and-the-tools-step-installs-the-skill", func() {
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": line})
		tlApply(t, ann, dir, tlTeamSetup, "tools", map[string]any{"tools": []string{"claude_code"}}).Says(t, skillDir)
		tlApply(t, ann, dir, tlTeamSetup, "secrets", map[string]any{"backend": "none"})
		tlApply(t, ann, dir, tlTeamSetup, "folders", map[string]any{"folders": []string{tlFolders}, "default": false})
		tlApply(t, ann, dir, tlTeamSetup, "this_folder", map[string]any{})
		tlApply(t, ann, dir, tlTeamSetup, "summary", map[string]any{})
		ann.Lore(dir, "sync").OK(t)
	})

	tt.w.Step("ann-is-healthy-right-after-joining", func() {
		tt.lore(tlAnn, "doctor").OK(t).Says(t, "Status: HEALTHY").Never(t, "DEGRADED", "tools.skill_installed", "not installed")
		tt.lore(tlAnn, "skill", "status", "--tool", "claude_code").OK(t).Says(t, "claude_code", skillDir).Never(t, "not installed")
	})
}
