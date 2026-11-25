//go:build journeys && !windows

package journeys

// agents_display_orgrepo_test.go — issue #177 (devco:D-986): in an org's
// own repo linked to a store folder, with .lore/ kept out of git, a Codex
// thread's actor row in the repo store had the shape an older lore wrote
// (no stable_key, no origin stamp) and read "<person> via codex". Once the
// person set `identity.agent_display: person`, every write of the thread to
// the repo store failed with "staged batch writes act_… twice": the key
// back-fill and the rename each updated the same row in one batch. Now the
// row is renamed and back-filled in one update and the write goes through.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyAgentDisplayOldRowInOrgRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	app := jPlainRepo(t, mira, w.Remote("github.com/agency-co/webapp"), filepath.Join(mira.Home, "work", "webapp"))
	actors := filepath.Join(app.Dir, ".lore", "data", "actors")
	agentRow := func() (string, map[string]any) {
		t.Helper()
		files, _ := os.ReadDir(actors)
		for _, f := range files {
			p := filepath.Join(actors, f.Name())
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var r map[string]any
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			if r["kind"] == "agent" {
				return p, r
			}
		}
		t.Fatalf("no agent row in %s", actors)
		return "", nil
	}

	w.Step("d986/org-own-repo-agent-writes-its-row", func() {
		mira.LoreIn(app, "init").OK(t) // the repo used lore before it was linked
		mira.LoreIn(app, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(app, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(app, "org", "link", "--own", "--default", "--project", "webapp", "--yes").OK(t)
		app.WriteFile(".gitignore", "/.lore/\n")
		app.CommitAll("ignore lore data")
		jAgentsRun(mira, app, &jCodex, "cx-d986", "tag", "add", "--name", "first", "--to", "repo").OK(t)
		if _, r := agentRow(); r["display_name"] != "Mira via codex" {
			t.Fatalf("agent row reads %v, want %q (the default)", r["display_name"], "Mira via codex")
		}
	})

	w.Step("d986/old-shaped-row-and-policy-person", func() {
		p, r := agentRow()
		for _, k := range []string{"stable_key", "handle", "origin_store", "origin_version", "label", "owner"} {
			delete(r, k) // the fields an older lore did not write
		}
		r["_w"] = map[string]any{"v": "0.1.18"}
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, _ := os.ReadFile(mira.LorePath("config.yaml"))
		mira.WriteHomeFile(".lore/config.yaml", string(cfg)+"policies:\n  identity.agent_display: person\n")
	})

	w.Step("d986/next-writes-rename-once-and-go-through", func() {
		jAgentsRun(mira, app, &jCodex, "cx-d986", "tag", "add", "--name", "second", "--to", "repo").OK(t).Never(t, "twice")
		_, r := agentRow()
		if r["display_name"] != "Mira" {
			t.Fatalf("agent row reads %v after the policy person, want %q", r["display_name"], "Mira")
		}
		if k, _ := r["stable_key"].(string); !strings.HasPrefix(k, "agent:") {
			t.Fatalf("agent row stable_key %v, want the agent's key back-filled", r["stable_key"])
		}
		jAgentsRun(mira, app, &jCodex, "cx-d986", "tag", "add", "--name", "third", "--to", "repo").OK(t)
	})
}
