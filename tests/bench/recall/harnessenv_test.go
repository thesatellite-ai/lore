package recall

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// TestHarnessEnvIsCleanAndAPerson: the environment the bench runs lore in
// carries no agent-detection variable of any tool even when the caller's
// environment has them all, and says LORE_HUMAN=1, so the setup writes are
// the bench person's when the bench runs inside an agent session, where
// lore's ancestor walk still sees the agent (devco:D-878).
func TestHarnessEnvIsCleanAndAPerson(t *testing.T) {
	agentVars := append(agentdetect.AllowedNames(nil), "LORE_ACTOR", "LORE_SESSION", "CODEX_THREAD_ID", "CODEX_SANDBOX")
	for _, name := range agentVars {
		t.Setenv(name, "1")
	}
	now := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	env := HarnessEnv("/h", "/b", "/t", now)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(agentVars, name) && name != "LORE_HUMAN" {
			t.Errorf("harness env carries the caller's agent variable %s", kv)
		}
	}
	for _, want := range []string{"HOME=/h", "TMPDIR=/t", "TZ=UTC", "LANG=C.UTF-8", "NO_COLOR=1", "LORE_HUMAN=1", "LORE_NOW=2026-03-04T00:00:00Z"} {
		if !slices.Contains(env, want) {
			t.Errorf("harness env misses %s: %v", want, env)
		}
	}
	if !strings.HasPrefix(env[slices.IndexFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") })], "PATH=/b:") {
		t.Errorf("PATH does not start with the lore bin folder: %v", env)
	}
	got, err := agentdetect.Detect(agentdetect.Inputs{Getenv: lookup(env)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tool != "" {
		t.Errorf("the harness env alone detects agent %q", got.Tool)
	}
	if e := HarnessEnv("/h", "/b", "/t", time.Time{}); slices.ContainsFunc(e, func(kv string) bool { return strings.HasPrefix(kv, "LORE_NOW=") }) {
		t.Errorf("a zero now still sets LORE_NOW: %v", e)
	}
}

// TestAgentEnvDropsOnlyLoreHuman: the wrapper an agent under test calls is
// the harness env without LORE_HUMAN, so its lore calls stay the agent's.
func TestAgentEnvDropsOnlyLoreHuman(t *testing.T) {
	env := HarnessEnv("/h", "/b", "/t", time.Time{})
	got := AgentEnv(env)
	if slices.Contains(got, "LORE_HUMAN=1") {
		t.Errorf("agent env still says LORE_HUMAN=1: %v", got)
	}
	if len(got) != len(env)-1 {
		t.Errorf("agent env dropped more than LORE_HUMAN: %v from %v", got, env)
	}
}

// lookup is a Getenv over a KEY=value list.
func lookup(env []string) func(string) string {
	return func(name string) string {
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				return v
			}
		}
		return ""
	}
}
