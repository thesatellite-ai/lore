//go:build journeys && !windows

package journeys

// agents_display_test.go — devco:D-900 (issue #113): a repo whose owner
// allows no tool name in its files. A Codex thread's note first puts
// "<person> via codex" into the committed actor row (the default is
// unchanged); the person then sets `identity.agent_display: person` for
// the repo, and the thread's next note renames that row to the person's
// name, with no other file under .lore/data naming the tool's prose.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyAgentDisplayRepoPolicy(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona(jAgentsName, jAgentsEmail)
	m := ada.Laptop()
	jAgentsIdentity(t, m)
	app := m.Clone(w.Remote(jAgentsRepo), "app")
	actors := filepath.Join(app.Dir, ".lore", "data", "actors")
	agentNames := func() []string {
		t.Helper()
		var out []string
		files, _ := os.ReadDir(actors)
		for _, f := range files {
			b, err := os.ReadFile(filepath.Join(actors, f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"kind": "agent"`) {
				for _, l := range strings.Split(string(b), "\n") {
					if strings.Contains(l, `"display_name"`) {
						out = append(out, strings.TrimSpace(l))
					}
				}
			}
		}
		return out
	}

	w.Step("d900/default-names-the-tool", func() {
		m.LoreIn(app, "init").OK(t)
		jAgentsAdd(t, m, app, &jCodex, "cx-d900", "codex: first note")
		if got := agentNames(); len(got) != 1 || !strings.Contains(got[0], jAgentsName+" via codex") {
			t.Fatalf("agent rows %q, want one reading %q (the default)", got, jAgentsName+" via codex")
		}
	})

	w.Step("d900/repo-policy-person-renames-the-row", func() {
		m.LoreIn(app, "policy", "set", "--key", "identity.agent_display", "--value", "person", "--to", "repo", "--yes").OK(t)
		jAgentsAdd(t, m, app, &jCodex, "cx-d900", "codex: second note")
		want := `"display_name": "` + jAgentsName + `",`
		if got := agentNames(); len(got) != 1 || got[0] != want {
			t.Fatalf("agent rows %q after the repo's policy person, want one %s (same actor, renamed)", got, want)
		}
	})
}
