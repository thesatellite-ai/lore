//go:build journeys && !windows

package journeys

// staff_remove_test.go — removing a member (B2-11, B2-12, B2-14). Lee
// adds Ann on @web; Ann joins and writes to the org store. Lee removes
// her with `lore member remove --yes` (one run, no second question);
// she is off @web. On Ann's laptop, after `lore sync`, the next write to
// the org's store is refused saying she left, while her personal store
// still takes writes. Lee undoes the removal the same day with `lore
// member add`; after a sync Ann writes to the org store again.

import (
	"testing"
)

func TestJourneyStaffRemove(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	orgStore := tlOrg + "/org"

	w.Step("day1/lee-adds-ann", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "team", "add", "@web", "--members", "@lee").OK(t)
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--name", tlAnn.Name, "--team", "@web", "--yes").OK(t)
	})

	w.Step("day1/ann-joins-and-writes", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "init").OK(t)
		tt.lore(tlAnn, "memory", "add", "ann's note before", "--to", orgStore, "--yes").OK(t)
	})

	w.Step("day1/lee-removes-ann", func() {
		tt.lore(tlLee, "member", "remove", tlAnn.Email, "--reason", "moved on", "--yes").OK(t).Says(t, "removed")
		tt.lore(tlLee, "member", "show", tlAnn.Email).OK(t).Says(t, "left").Never(t, "@web")
		tt.lore(tlLee, "team", "show", "@web").OK(t).Never(t, tlAnn.Name)
	})

	w.Step("day1/ann-next-write-is-refused", func() {
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "memory", "add", "ann's note after", "--to", orgStore, "--yes").
			Refused(t, "E_REFUSED", "you are marked as left in "+tlOrg, "lore member add")
		tt.lore(tlAnn, "memory", "add", "my own note", "--to", "me", "--yes").OK(t)
	})

	w.Step("day1/lee-undoes-it-the-same-day", func() {
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--team", "@web", "--yes").OK(t).Says(t, "back on the roster")
		tt.lore(tlLee, "member", "show", tlAnn.Email).OK(t).Says(t, "active", "@web")
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "memory", "add", "ann's note back", "--to", orgStore, "--yes").OK(t)
	})
}
