//go:build journeys && !windows

package journeys

// join_before_sync_test.go — the first day of a new team, before the
// admin's first lore sync (devco:K-1, devco:L-1, devco:J-1). Ann applies
// the join step on a fresh laptop: refused naming the member plan, never
// the solo one. She saves the member steps and joins while Lee's org is
// still only on Lee's machine: refused with "the admin has not sent it"
// and lore sync for the admin, never "commit lore-org.yaml"; her folders
// step says it waits for the join. A tools answer outside the offered
// tools is a field error, never a broken config.yaml. After Lee adds Ann
// and syncs, the same join works.

import (
	"testing"
)

func TestJourneyJoinBeforeAdminSync(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	leeDir := tt.repo[tlLee.Email].Dir
	url := tt.store.SSHURL()
	join := `{"invite":"` + url + `"}`

	w.Step("day1/lee-makes-the-team-without-sync", func() {
		tlFirstSteps(t, lee, leeDir, tlLeadSetup, "admin", tlLee)
		tlApply(t, lee, leeDir, tlLeadSetup, "team", map[string]any{"org": tlOrg, "store_url": url})
		lee.Lore(leeDir, "setup", "apply", tlLeadSetup, "--step", "tools", "--answers", `{"tools":["claude"]}`).
			Refused(t, "E_INVALID_INPUT", `"claude" is not one of`, "claude_code").
			Never(t, "E_CONFIG_INVALID", "config.yaml")
	})

	w.Step("day1/ann-joins-too-early", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		ann.Lore(dir, "setup", "apply", tlTeamSetup, "--step", "join", "--answers", join, "--yes").
			Refused(t, `step "join" is in the member plan`, `--step kind --answers '{"kind":"member"}'`).
			Never(t, "solo")
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		ann.Lore(dir, "setup", "apply", tlTeamSetup, "--step", "join", "--answers", join, "--yes").
			Refused(t, "the admin who made the org has not sent it", "ask the org's admin to run lore sync").
			Never(t, "lore-org.yaml", "<org_folder>")
		ann.Lore(dir, "setup", "apply", tlTeamSetup, "--step", "folders", "--answers", `{"folders":["`+tlFolders+`"]}`, "--yes").OK(t).
			Says(t, "takes effect once its join step is done")
	})

	w.Step("day1/after-lee-syncs-the-join-works", func() {
		tlLastSteps(t, lee, leeDir, tlLeadSetup, []string{tlFolders})
		lee.Lore(leeDir, "sync").OK(t)
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--name", tlAnn.Name, "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		ann := tt.m[tlAnn.Email]
		ann.Lore(tt.repo[tlAnn.Email].Dir, "setup", "apply", tlTeamSetup, "--step", "join", "--answers", join, "--yes").OK(t)
	})
}
