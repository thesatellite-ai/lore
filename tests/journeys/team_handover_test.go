//go:build journeys && !windows

package journeys

// team_handover_test.go — an admin runs the org from the home folder and
// hands a leaver's work over (B-7, B-12). Lee adds a company rule with
// `--to <org>/org` from ~ (no repo there: it used to fail with "run git
// init here first"), assigns two tasks to Ann, finds them with
// `task list --assignee @ann`, removes Ann (the output lists her open tasks
// and the hand-over line), and runs that line: her handle and email still
// resolve after she left.

import "testing"

// Names of the hand-over journey (placeholders).
const (
	jhoRule     = "every estimate is reviewed by two people"
	jhoTaskOne  = "handover: finish the billing retry"
	jhoTaskTwo  = "handover: write the retry runbook"
	jhoFolders  = "~/**"
	jhoAnn      = "ann"
	jhoBob      = "bob"
	jhoHandover = "lore task reassign --from @ann --assign @<someone> --yes"
)

func TestJourneyTeamHandover(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)

	w.Step("day1/admin-starts-the-org", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{jhoFolders})
		tt.lore(tlLee, "init").OK(t)
		lee.Lore(lee.Home, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", jhoAnn, "--yes").OK(t)
		lee.Lore(lee.Home, "member", "add", "--email", tlBob.Email, "--name", tlBob.Name, "--handle", jhoBob, "--yes").OK(t)
	})

	w.Step("day1/company-rule-from-the-home-folder", func() {
		lee.Lore(lee.Home, "rule", "add", "--to", tlOrg+"/org", "--body", jhoRule).OK(t).Never(t, "git init")
		// no --to: still a repo command, and the hint in ~ names --to
		lee.Lore(lee.Home, "memory", "add", "--body", "no target").Fails(t, 19, "--to").Never(t, "git init")
		tt.lore(tlLee, "rule", "list").OK(t).Says(t, jhoRule)
	})

	w.Step("week2/tasks-for-ann", func() {
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		tt.lore(tlLee, "task", "add", "--title", jhoTaskOne, "--assign", "@"+jhoAnn).OK(t)
		tt.lore(tlLee, "task", "add", "--title", jhoTaskTwo, "--assign", "@"+jhoAnn).OK(t)
		tt.lore(tlLee, "task", "list", "--assignee", "@"+jhoAnn).OK(t).Says(t, jhoTaskOne, jhoTaskTwo)
	})

	w.Step("week3/ann-leaves-and-her-work-is-handed-over", func() {
		tt.lore(tlLee, "member", "remove", "--who", "@"+jhoAnn, "--reason", "left the company", "--yes").OK(t).
			Says(t, "open tasks (2)", jhoTaskOne, jhoTaskTwo, jhoHandover)
		tt.lore(tlLee, "task", "list", "--assignee", tlAnn.Email).OK(t).Says(t, jhoTaskOne, jhoTaskTwo)
		tt.lore(tlLee, "task", "reassign", "--from", "@"+jhoAnn, "--assign", "@"+jhoBob, "--yes").OK(t).
			Says(t, "2 tasks reassigned to @"+jhoBob)
		tt.lore(tlLee, "task", "list", "--assignee", "@"+jhoBob).OK(t).Says(t, jhoTaskOne, jhoTaskTwo)
		tt.lore(tlLee, "task", "reassign", "--from", "nobody@example.test", "--assign", "@"+jhoBob, "--yes").
			Fails(t, 27, "no one is nobody@example.test on the roster").Never(t, "@nobody@")
	})
}
