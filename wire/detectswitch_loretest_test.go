//go:build loretest

package wire

import "testing"

// TestDetectAgentOffInTestBuild: in a loretest build LORE_NO_DETECT=1
// makes the real detector see nothing, even inside a coding-agent session
// (tool markers in the environment, an agent among the ancestors).
func TestDetectAgentOffInTestBuild(t *testing.T) {
	t.Setenv("LORE_NO_DETECT", "1")
	t.Setenv("CODEX_THREAD_ID", "t-1")
	r, err := DetectAgent()
	if err != nil {
		t.Fatal(err)
	}
	if r.IsAgent() || r.Tool != "" || r.CI != nil {
		t.Fatalf("LORE_NO_DETECT=1 still detected %+v", r)
	}
}
