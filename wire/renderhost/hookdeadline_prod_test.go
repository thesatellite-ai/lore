//go:build !loretest

package renderhost

import "testing"

// TestHookDeadlineReleaseIgnoresEnv: LORE_HOOK_TIMEOUT is a test-build
// seam; a release binary keeps the 6.9 deadline whatever the environment
// says, so no hook can be made to block the tool.
func TestHookDeadlineReleaseIgnoresEnv(t *testing.T) {
	t.Setenv("LORE_HOOK_TIMEOUT", "10m")
	if got := hookDeadline(); got != hookTimeout {
		t.Fatalf("release hook deadline = %v with LORE_HOOK_TIMEOUT set, want %v", got, hookTimeout)
	}
}
