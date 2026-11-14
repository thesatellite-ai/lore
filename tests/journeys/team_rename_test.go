//go:build journeys && !windows

package journeys

// team_rename_test.go — a team's work and its handle (bug hunt G-15). Lee
// makes @web with Ann, assigns a task to the team and finds it in his
// inbox, not only in task list --mine; renames @web to @frontend (the task
// follows, no second team); then removes a team and assigning to it is
// refused naming the team list and the active team with the same members.

import "testing"

func TestJourneyTeamRename(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	const task = "web: fix login redirect"

	w.Step("day1/team-task-reaches-the-inbox", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{"~/**"})
		tt.lore(tlLee, "init").OK(t)
		lee.Lore(lee.Home, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--yes").OK(t)
		tt.lore(tlLee, "team", "add", "--handle", "@web", "--members", "@lee,@ann").OK(t)
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		tt.lore(tlLee, "task", "add", "--title", task, "--assign", "@web").OK(t)
		tt.lore(tlLee, "task", "list", "--mine").OK(t).Says(t, task)
		tt.lore(tlLee, "inbox").OK(t).Says(t, task, "(team @web)").Never(t, "inbox empty")
	})

	w.Step("day2/rename-the-handle", func() {
		tt.lore(tlLee, "team", "rename", "--handle", "@web", "--to", "@frontend").OK(t).Says(t, "@web is now @frontend", "tasks assigned to it follow")
		tt.lore(tlLee, "team", "rename", "--handle", "@frontend", "--to", "@ann").Fails(t, 20, "a team and a person cannot share one")
		tt.lore(tlLee, "team", "list", "--all").OK(t).Says(t, "@frontend").Never(t, "@web")
		tt.lore(tlLee, "inbox").OK(t).Says(t, task, "(team @frontend)")
	})

	w.Step("day3/assign-to-a-removed-team", func() {
		tt.lore(tlLee, "team", "add", "--handle", "@ui", "--members", "@lee,@ann").OK(t)
		tt.lore(tlLee, "team", "remove", "--handle", "@frontend", "--yes").OK(t)
		tt.lore(tlLee, "task", "add", "--title", "web again", "--assign", "@frontend").
			Fails(t, 27, "team @frontend was removed", "@ui has the same members", "lore team list")
	})
}
