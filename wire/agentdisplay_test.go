package wire

// agentdisplay_test.go — issue #113 (devco:D-900): identity.agent_display
// from the env, config.yaml's policies and the opened engine's floor; the
// stricter wins, and migrated_via and the Lore-Agent trailer follow it.
// Made-up people only.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
)

func TestAgentDisplayDecidesTheWrittenName(t *testing.T) {
	agentEnv(t)
	ctx := context.Background()
	cfg := "identity: {key: \"human:alice@example.com\", name: Alice Example}\n"
	resolve := func(t *testing.T, config, env string, floor identity.AgentDisplay) identity.Resolved {
		t.Helper()
		t.Setenv(identity.EnvAgentDisplay, env)
		home := t.TempDir()
		writeConfig(t, home, config)
		opts := Options{Home: home, RepoDir: t.TempDir(), agentDisplay: &agentDisplayFloor{}}
		opts.agentDisplay.raise(floor)
		who, err := resolveIdentity(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if who.Agent == "" {
			t.Fatalf("detection did not see the agent (the case under test): %+v", who)
		}
		return who
	}
	cases := []struct {
		name, config, env string
		floor             identity.AgentDisplay
		want              string
	}{
		{"default names the tool", cfg, "", "", "Alice Example via "},
		{"env agent", cfg, "agent", "", "Alice Example (agent)"},
		{"config policy person", cfg + "policies:\n  identity.agent_display: person\n", "", "", "Alice Example"},
		{"env cannot loosen config", cfg + "policies:\n  identity.agent_display: person\n", "tool", "", "Alice Example"},
		{"engine floor person", cfg, "agent", identity.AgentDisplayPerson, "Alice Example"},
		{"floor cannot loosen env", cfg, "person", identity.AgentDisplayAgent, "Alice Example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			who := resolve(t, c.config, c.env, c.floor)
			if strings.HasSuffix(c.want, " via ") {
				if !strings.HasPrefix(who.DisplayName, c.want) {
					t.Fatalf("display %q, want %q<tool>", who.DisplayName, c.want)
				}
				return
			}
			if who.DisplayName != c.want {
				t.Fatalf("display %q, want %q", who.DisplayName, c.want)
			}
			if _, via := migratedBy(who); via != migratedViaAgent {
				t.Errorf("migrated_via %q names the tool", via)
			}
		})
	}
	t.Run("bad env value is said and ignored", func(t *testing.T) {
		who := resolve(t, cfg, "vendor", "")
		if !strings.Contains(who.DisplayName, " via ") || !strings.Contains(strings.Join(who.Notes, ";"), identity.EnvAgentDisplay+"=vendor ignored") {
			t.Fatalf("%+v", who)
		}
	})
}

// TestMigrateRunHidesARecordedTool: a migrated_via written before the
// setting hid the tool is rewritten by migrate run, said first under
// --dry-run; every other key is kept.
func TestMigrateRunHidesARecordedTool(t *testing.T) {
	agentEnv(t)
	t.Setenv(identity.EnvAgentDisplay, "")
	home := t.TempDir()
	writeConfig(t, home, "identity: {key: \"human:alice@example.com\", name: Alice Example}\n")
	top := gitRepo(t)
	data := filepath.Join(top, ".lore", "data")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, rowfs.MetaFileName), []byte(`{"_v": 1, "project_id": "p"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wall := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	a := &App{opts: Options{Home: home, RepoDir: top, Wall: func() time.Time { return wall }}}
	ctx := context.Background()
	if _, err := a.raiseProjectFormat(ctx, top); err != nil {
		t.Fatal(err)
	}
	pf, err := ProjectFormatAt(top)
	if err != nil || !strings.Contains(pf.MigratedVia, "agent:auto/") {
		t.Fatalf("the default run did not record the tool (the case under test): %+v %v", pf, err)
	}
	t.Setenv(identity.EnvAgentDisplay, "tool")
	if steps, err := a.migrateMigratedByEmail(ctx, false); err != nil || len(steps) != 0 {
		t.Fatalf("the default rewrote migrated_via: %q %v", steps, err)
	}
	// an older value naming the tool, then migrate run's step with the setting on
	if _, err := rowfs.WriteProjectFormat(top, pf.Format, rowfs.ProjectMigration{By: pf.MigratedBy, Via: "codex (agent:auto/codex/abc123)", At: pf.MigratedAt}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(identity.EnvAgentDisplay, "agent")
	steps, err := a.migrateMigratedByEmail(ctx, true)
	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "would keep migrated_via \"agent\"") {
		t.Fatalf("dry run: %q %v", steps, err)
	}
	if pf, _ = ProjectFormatAt(top); !strings.Contains(pf.MigratedVia, "codex") {
		t.Fatalf("the dry run wrote: %+v", pf)
	}
	steps, err = a.migrateMigratedByEmail(ctx, false)
	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "now keeps migrated_via \"agent\"") {
		t.Fatalf("run: %q %v", steps, err)
	}
	if pf, _ = ProjectFormatAt(top); pf.MigratedVia != migratedViaAgent || pf.MigratedBy != "Alice Example" || pf.MigratedAt == "" {
		t.Fatalf("after: %+v", pf)
	}
	if steps, _ = a.migrateMigratedByEmail(ctx, false); len(steps) != 0 {
		t.Fatalf("a second run rewrote again: %q", steps)
	}
}

// TestAgentDisplayPolicyReadOnlyForDetectedAgents: the open reads the
// engine's identity.agent_display (a List of every attached store's
// policies) only for a detected agent that does not already hide the most;
// a person, a named actor and an agent already at person never pay for it
// (a person's command lists no store at open for it).
func TestAgentDisplayPolicyReadOnlyForDetectedAgents(t *testing.T) {
	agentEnv(t)
	t.Setenv(identity.EnvAgentDisplay, "")
	ctx := context.Background()
	home := t.TempDir()
	writeConfig(t, home, "identity: {key: \"human:alice@example.com\", name: Alice Example}\n")
	resolve := func(opts Options) identity.Resolved {
		t.Helper()
		who, err := resolveIdentity(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		return who
	}
	agent := resolve(Options{Home: home, RepoDir: t.TempDir()})
	if agent.Step != identity.StepDetected {
		t.Fatalf("detection did not see the agent (the case under test): %+v", agent)
	}
	if !needsAgentDisplayPolicy(agent) {
		t.Fatal("a detected agent naming its tool must read the engine's policy")
	}
	if needsAgentDisplayPolicy(identity.WithAgentDisplay(agent, identity.AgentDisplayPerson)) {
		t.Fatal("an agent already at person read the policy again")
	}
	flag := resolve(Options{Home: home, RepoDir: t.TempDir(), FlagActor: "human:alice@example.com"})
	if flag.Step == identity.StepDetected || needsAgentDisplayPolicy(flag) {
		t.Fatalf("a named actor reads the engine's policy at open: %+v", flag)
	}
	if needsAgentDisplayPolicy(identity.Resolved{StableKey: "human:alice@example.com"}) {
		t.Fatal("a person reads the engine's policy at open")
	}
}
