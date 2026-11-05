//go:build !loretest

package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// TestDetectAgentIgnoresNoDetect: a release build ignores LORE_NO_DETECT;
// a tool marker in the environment is still detected (AGENT_IDENTITY 10.6).
// The detector reads a fixed environment (only the codex marker), no
// terminal and no ancestor walk, so the result does not depend on the shell
// running the test: inside tmux or an agent session the real inputs would
// add a TTY and a walk without codex, which 10.4 reads as a stale marker.
func TestDetectAgentIgnoresNoDetect(t *testing.T) {
	t.Setenv("LORE_NO_DETECT", "1")
	env := map[string]string{"CODEX_THREAD_ID": "t-1"}
	r, err := detectAgent(func() agentdetect.Inputs {
		return agentdetect.Inputs{Getenv: func(k string) string { return env[k] }}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsAgent() || r.Tool != "codex" {
		t.Fatalf("release build hid the agent: %+v", r)
	}
}
