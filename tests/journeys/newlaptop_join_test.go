//go:build journeys && !windows

package journeys

// newlaptop_join_test.go — a new laptop with only git access (B-14). Ann
// clones the team's app on a machine where lore has no setup: whoami
// (and doctor, in a repo with lore data) say how to join the org instead
// of only "org none".

import "testing"

// jnlJoin is the command the join line names.
const jnlJoin = "lore setup join --invite"

func TestJourneyNewLaptopJoinHint(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	lee := tt.person(tlLee)
	tt.person(tlAnn)

	tt.w.Step("day1/lead-starts-the-team", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
	})

	tt.w.Step("day2/a-clone-on-a-new-laptop-says-how-to-join", func() {
		tt.lore(tlAnn, "whoami").OK(t).Says(t, "org        none", jnlJoin, "lore member add")
		tt.lore(tlLee, "whoami").OK(t).Never(t, jnlJoin)
	})
}
