//go:build !loretest

package identity

import (
	"context"
	"testing"
)

// TestNoDetectIgnoredInRelease: a release build has no LORE_NO_DETECT
// knob (AGENT_IDENTITY 10.6): with the variable set, a coding-agent session
// still resolves to the detected agent, so a shipped binary can never be
// told to hide an agent.
func TestNoDetectIgnoredInRelease(t *testing.T) {
	t.Setenv("LORE_NO_DETECT", "1")
	if DetectionOff() {
		t.Fatal("release build honours LORE_NO_DETECT")
	}
	env := map[string]string{"CODEX_THREAD_ID": "t-1", "LORE_NO_DETECT": "1"}
	in := Inputs{Home: t.TempDir(), Getenv: func(k string) string { return env[k] },
		Config: Config{Identity: Entry{Key: "human:tomas@agency.example", Name: "tomas"}}, Detect: detectWith(env, true)}
	r, err := Resolve(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindAgent || r.Agent == "" {
		t.Fatalf("release build with LORE_NO_DETECT=1 resolved %+v, want the detected agent", r)
	}
}
