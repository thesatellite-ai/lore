//go:build journeys && !windows

package journeys

// agent_setup_actor_test.go — devco D-440: Ada's wrapper script runs the
// admin setup through her coding-agent session with LORE_ACTOR=agent:<label>
// set. Before the fix the team step of the new org refused the agent ("no
// person behind it") only after it had made and committed the org's store
// clone: a half-made org. Now a team step that is refused (here: a store
// URL nobody can reach) leaves nothing behind (no clone, no org entry, no
// push), the retry with the right URL completes, and every row and commit
// of the setup is Ada's: no agent actor on the roster and no Lore-Agent
// trailer.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jSALabel is the agent label Ada's wrapper sets (placeholder).
const jSALabel = "store-builder"

func TestJourneyAgentSetupUnderLoreActor(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	ada := tlPerson{jAgentsName, jAgentsEmail, jAgentsLogin}
	m := tt.person(ada)
	store := w.Remote("github.com/wrapper-co/lore-actor")
	agent := jAgentsAs(tt.repo[ada.Email], &jClaude, "cc-actor").With("LORE_ACTOR=agent:" + jSALabel)
	apply := func(step string, answers any) string {
		t.Helper()
		r := agent.Lore("setup", "apply", jAdminSetup, "--step", step, "--answers", tlAnswers(t, answers))
		if r.Code != 0 {
			t.Fatalf("Ada's agent saves setup step %s: %s", step, r)
		}
		return r.Out()
	}
	// state is what a team step makes on Ada's machine: the store clones
	// under ~/.lore/stores (three levels) and the config.
	state := func() string {
		var out []string
		root := m.LorePath("stores")
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			if strings.Count(rel, string(filepath.Separator)) >= 2 && d.IsDir() {
				out = append(out, rel)
				return filepath.SkipDir
			}
			out = append(out, rel)
			return nil
		})
		cfg, _ := os.ReadFile(m.LorePath("config.yaml"))
		return strings.Join(out, "\n") + "\n--config--\n" + string(cfg)
	}
	var before string

	w.Step("day1/first-steps-through-the-wrapper", func() {
		apply("kind", map[string]any{"kind": "admin", "name": jAdminSetup})
		apply("identity", map[string]any{"name": ada.Name, "email": ada.Email})
		apply("github", map[string]any{"github_account": ada.Login, "host_alias": "github.com"})
		apply("personal_store", map[string]any{"personal_store": "none"})
	})

	w.Step("day1/a-refused-team-step-leaves-nothing-half-made", func() {
		before = state()
		bad := strings.Replace(store.SSHURL(), "lore-actor", "no-such-store", 1)
		r := agent.Lore("setup", "apply", jAdminSetup, "--step", "team", "--answers",
			tlAnswers(t, map[string]any{"org": jAdminOrg, "store_url": bad, "push": "now"}))
		if r.Code == 0 {
			t.Fatalf("a team step on a store nobody can reach succeeded: %s", r)
		}
		if after := state(); after != before {
			t.Errorf("the refused team step left something behind:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if refs := w.Git(store.Dir, "for-each-ref"); strings.TrimSpace(refs) != "" {
			t.Errorf("the refused team step pushed to the org store remote:\n%s", refs)
		}
	})

	w.Step("day1/the-retry-completes-credited-to-ada", func() {
		said := apply("team", map[string]any{"org": jAdminOrg, "store_url": store.SSHURL(), "push": "now"})
		if !strings.Contains(said, "credited to "+ada.Name) {
			t.Errorf("the team step run with LORE_ACTOR=agent:%s must say it credits %s:\n%s", jSALabel, ada.Name, said)
		}
		apply("tools", map[string]any{"tools": []string{jAgentsClaude}})
		apply("secrets", map[string]any{"backend": "none"})
		apply("folders", map[string]any{"folders": []string{tlFolders}, "default": true})
		roster := tt.roster(t, ada)
		if _, ok := roster["human:"+ada.Email]; !ok {
			t.Fatalf("Ada is not on the roster: %+v", roster)
		}
		for key := range roster {
			if strings.HasPrefix(key, "agent:") {
				t.Errorf("the setup under LORE_ACTOR=agent:%s registered the agent actor %s", jSALabel, key)
			}
		}
		logs := w.Git(store.Dir, "log", "--format=%an <%ae>%n%B", "main")
		if strings.Contains(logs, "Lore-Agent:") {
			t.Errorf("the org store's setup commits carry a Lore-Agent trailer:\n%s", logs)
		}
		if !strings.Contains(logs, ada.Email) {
			t.Errorf("the org store's setup commits are not authored by %s:\n%s", ada.Email, logs)
		}
		// the state the refusal check compares does show a team step
		if after := state(); after == before || !strings.Contains(after, jAdminOrg) {
			t.Errorf("the completed team step changed nothing the refusal check looks at:\n%s", after)
		}
		m.LoreIn(tt.repo[ada.Email], "skill", "install", "--tool", jAgentsClaude).OK(t)
		m.LoreIn(tt.repo[ada.Email], "doctor").OK(t)
	})
}
