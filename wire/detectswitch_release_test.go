//go:build !loretest

package wire

import "testing"

// TestDetectAgentIgnoresNoDetect: a release build ignores LORE_NO_DETECT;
// a tool marker in the environment is still detected (AGENT_IDENTITY 10.6).
func TestDetectAgentIgnoresNoDetect(t *testing.T) {
	t.Setenv("LORE_NO_DETECT", "1")
	t.Setenv("CODEX_THREAD_ID", "t-1")
	r, err := DetectAgent()
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsAgent() {
		t.Fatalf("release build hid the agent: %+v", r)
	}
}
