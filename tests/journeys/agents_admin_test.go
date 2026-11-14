//go:build journeys && !windows

package journeys

// agents_admin_test.go — a person's coding agent starts a team for them
// (owner rule, 2026-11-11): a coding agent acts for its person and may do
// anything the person tells it. Ada asks her Claude Code session to run
// the whole admin setup, add a team folder and invite a teammate. Every
// step passes with no policy set; the writes are Ada's (she is the org's
// admin and on its roster), and the rows record "Ada via claude_code".
// Then Ada, in her own terminal, opts the team into strictness, and the
// same agent steps are refused with the policy named.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ada's team (placeholders).
const (
	jAdminOrg       = "solverco"
	jAdminSetup     = "solverco"
	jAdminStoreSlug = "github.com/solver-co/lore-team"
	jAdminFolder    = "design"
	jAdminTeammate  = "bea@example.test"
)

func TestJourneyAgentsAdminSetup(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	ada := tlPerson{jAgentsName, jAgentsEmail, jAgentsLogin}
	m := tt.person(ada)
	store := w.Remote(jAdminStoreSlug)
	dir := tt.repo[ada.Email].Dir
	agent := jAgentsAs(tt.repo[ada.Email], &jClaude, "cc-admin")
	// apply saves one setup step from Ada's Claude Code session.
	apply := func(step string, answers any) {
		t.Helper()
		b := tlAnswers(t, answers)
		if r := agent.Lore("setup", "apply", jAdminSetup, "--step", step, "--answers", b, "--json"); r.Code != 0 {
			c := agent.Lore("setup", "check", jAdminSetup, "--step", step, "--answers", b, "--json")
			t.Fatalf("Ada's agent saves setup step %s: %s\ncheck says: %s", step, r, c.Stdout)
		}
	}

	w.Step("day1/agent-runs-the-admin-setup", func() {
		apply("kind", map[string]any{"kind": "admin", "name": jAdminSetup})
		apply("identity", map[string]any{"name": ada.Name, "email": ada.Email})
		apply("github", map[string]any{"github_account": ada.Login, "host_alias": "github.com"})
		apply("personal_store", map[string]any{"personal_store": "none"})
		apply("team", map[string]any{"org": jAdminOrg, "store_url": store.SSHURL(), "push": "now"})
		apply("tools", map[string]any{"tools": []string{jAgentsClaude}})
		apply("secrets", map[string]any{"backend": "none"})
		apply("folders", map[string]any{"folders": []string{tlFolders}, "default": true})

		// Ada (the setup's identity) is the org's admin and on its roster,
		// as herself: the agent registered her, not itself.
		tt.lore(ada, "setup", "list").OK(t).Says(t, jAdminSetup, "admin", jAdminOrg)
		roster := tt.roster(t, ada)
		me, ok := roster["human:"+ada.Email]
		if !ok || me.Display != ada.Name {
			t.Errorf("Ada is not on %s's roster as herself after her agent's team step: %+v", jAdminOrg, roster)
		}
		// D-17: the agent session's own actor row goes into the org store
		// (it signs the rows it writes), but only as Ada's agent: never a
		// person, never a member of its own.
		for key, a := range roster {
			if strings.HasPrefix(key, "agent:") && (a.Kind != "agent" || a.Owner != me.ID) {
				t.Errorf("the team step registered the agent %s as %+v; it acts as Ada (an agent row owned by %s)", key, a, me.ID)
			}
		}
		if !store.Has("refs/heads/main") {
			t.Error("the team store has no main branch after the agent's team step")
		}

		agent.Lore("store", "folder", "add", jAdminOrg, jAdminFolder).OK(t).Says(t, jAdminFolder)
		if len(store.FilesMatching("refs/heads/main", jAdminFolder+"/")) == 0 {
			t.Errorf("the team store has no %s folder after the agent's store folder add", jAdminFolder)
		}
		inv := agent.Lore("team", "invite", jAdminTeammate, "--yes", "--json").OK(t)
		if !strings.Contains(inv.Stdout, "lore-invite:") {
			t.Errorf("the agent's team invite printed no invite line: %s", inv)
		}
		// The agent starts a labelled agent for Ada: its owner is Ada.
		agent.Lore("agent", "start", "--label", "helper").OK(t).Says(t, "helper")
		// An agent actor with no person behind it is the one refusal, and
		// it names the fix.
		ghost := tt.repo[ada.Email].With("LORE_ACTOR=agent:ghost")
		ghost.Lore("actor", "register").Refused(t).Says(t, "agent:ghost has no person behind it", "--owner @<person>")

		// Provenance: the invite records Ada's Claude Code ("Ada via
		// claude_code") as the writer, as every write of it does.
		who := jAgentsWhoami(t, m, tt.repo[ada.Email], &jClaude, "cc-admin").Data.Identity
		if who.Kind != "agent" || who.Name != ada.Name+" via "+jAgentsClaude {
			t.Errorf("whoami in Ada's Claude Code session = %+v, want %s via %s", who, ada.Name, jAgentsClaude)
		}
		tt.lore(ada, "team", "invite", "list").OK(t).Says(t, jAdminTeammate, who.ActorID)
	})

	w.Step("day2/ada-opts-the-team-into-strictness", func() {
		w.AdvanceDays(1)
		// Ada, in her own terminal: the team's agents may not manage
		// people, and on her machine only a person adds a store folder
		// (store verbs read this machine's policies: level).
		tt.lore(ada, "policy", "set", "agents.may_manage_people", "false", "--to", jAdminOrg+"/org", "--yes").OK(t)
		cfg := filepath.Join(m.Home, ".lore", "config.yaml")
		b, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(cfg, append(b, []byte("policies:\n  agents.person_only: [store.folder.add]\n")...), 0o600); err != nil {
			t.Fatal(err)
		}

		team := tlAnswers(t, map[string]any{"org": jAdminOrg, "store_url": store.SSHURL(), "push": "now"})
		agent.Lore("setup", "apply", jAdminSetup, "--step", "team", "--answers", team, "--json").Refused(t).Says(t, "agents.may_manage_people is false")
		agent.Lore("team", "invite", "cy@example.test", "--yes").Refused(t).Says(t, "agents.may_manage_people is false")
		agent.Lore("agent", "start", "--label", "helper2").Refused(t).Says(t, "agents.may_manage_people is false")
		agent.Lore("store", "folder", "add", jAdminOrg, "notes").Refused(t).Says(t, "agents.person_only lists store.folder.add")

		// The same steps from Ada's own terminal pass.
		tlApply(t, m, dir, jAdminSetup, "team", map[string]any{"org": jAdminOrg, "store_url": store.SSHURL(), "push": "now"})
		tt.lore(ada, "team", "invite", "cy@example.test", "--yes").OK(t)
		tt.lore(ada, "store", "folder", "add", jAdminOrg, "notes").OK(t)
		tt.lore(ada, "agent", "start", "--label", "helper2").OK(t)
	})
}

// tlAnswers is a setup step's answers as the --answers JSON.
func tlAnswers(t testing.TB, answers any) string {
	t.Helper()
	b, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
