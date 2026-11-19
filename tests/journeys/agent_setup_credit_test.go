//go:build journeys && !windows

package journeys

// agent_setup_credit_test.go — devco D-424: Ada runs the admin setup
// through her Claude Code session before any agent label is chosen. The
// org's first rows and commits are hers: no "Ada via claude_code" actor
// on the roster, no Lore-Agent trailer, and the setup says whom it
// credited. Her agent's later writes do register its actor; Ada merges it
// into herself from a folder that is no repo (with and without --store),
// with no history rewrite and no force push.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyAgentSetupCreditsThePerson(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	ada := tlPerson{jAgentsName, jAgentsEmail, jAgentsLogin}
	m := tt.person(ada)
	store := w.Remote("github.com/solver-co/lore-credit")
	agent := jAgentsAs(tt.repo[ada.Email], &jClaude, "cc-setup")
	apply := func(step string, answers any) string {
		t.Helper()
		b := tlAnswers(t, answers)
		r := agent.Lore("setup", "apply", jAdminSetup, "--step", step, "--answers", b)
		if r.Code != 0 {
			t.Fatalf("Ada's agent saves setup step %s: %s", step, r)
		}
		return r.Out()
	}
	var adaID string

	w.Step("day1/agent-runs-the-admin-setup", func() {
		apply("kind", map[string]any{"kind": "admin", "name": jAdminSetup})
		apply("identity", map[string]any{"name": ada.Name, "email": ada.Email})
		apply("github", map[string]any{"github_account": ada.Login, "host_alias": "github.com"})
		apply("personal_store", map[string]any{"personal_store": "none"})
		said := apply("team", map[string]any{"org": jAdminOrg, "store_url": store.SSHURL(), "push": "now"})
		if !strings.Contains(said, "credited to "+ada.Name) {
			t.Errorf("the team step run through claude_code must say it credits %s:\n%s", ada.Name, said)
		}
		apply("tools", map[string]any{"tools": []string{jAgentsClaude}})
		apply("secrets", map[string]any{"backend": "none"})
		apply("folders", map[string]any{"folders": []string{tlFolders}, "default": true})
		roster := tt.roster(t, ada)
		me, ok := roster["human:"+ada.Email]
		if !ok {
			t.Fatalf("Ada is not on the roster: %+v", roster)
		}
		adaID = me.ID
		for key := range roster {
			if strings.HasPrefix(key, "agent:") {
				t.Errorf("the setup registered the agent actor %s before any agent label was chosen", key)
			}
		}
		if logs := w.Git(store.Dir, "log", "--format=%B", "main"); strings.Contains(logs, "Lore-Agent:") {
			t.Errorf("the org store's setup commits carry a Lore-Agent trailer:\n%s", logs)
		}
	})

	var agentID string
	w.Step("day1/the-agent-writes-for-ada", func() {
		agent.Lore("team", "invite", jAdminTeammate, "--yes").OK(t)
		agentID = jAgentsWhoami(t, m, tt.repo[ada.Email], &jClaude, "cc-setup").Data.Identity.ActorID
		found := false
		for _, a := range tt.roster(t, ada) {
			found = found || (a.ID == agentID && a.Owner == adaID)
		}
		if !found {
			t.Fatalf("the agent's own write registered no actor %s owned by Ada (%s): %+v", agentID, adaID, tt.roster(t, ada))
		}
	})

	w.Step("day2/ada-merges-her-agent-into-herself-from-anywhere", func() {
		w.AdvanceDays(1)
		away := filepath.Join(m.Home, "notes-not-a-repo")
		before := len(store.Log("refs/heads/main"))
		m.Lore(away, "actor", "merge", "--from", agentID, "--into", adaID, "--dry-run").OK(t)
		m.Lore(away, "actor", "merge", "--from", agentID, "--into", adaID, "--store", jAdminOrg+"/org", "--yes").OK(t).Says(t, "merged")
		m.Lore(away, "sync").OK(t)
		if after := len(store.Log("refs/heads/main")); after <= before {
			t.Errorf("the merge must append commits to the org store (no rewrite): %d commits before, %d after", before, after)
		}
		for _, a := range tt.roster(t, ada) {
			if a.ID == agentID && a.Status != "left" {
				t.Errorf("the merged agent actor still reads as active: %+v", a)
			}
		}
	})
}
