package wire

import (
	"strings"
	"testing"
)

// TestUncoveredStoreClaimsNamesTheCoveringDaemon pins devco:D-673: a code
// repo daemon that leaves org store claims out says so at start, naming
// each claim and the daemon command (named flags) that renews it right
// there.
func TestUncoveredStoreClaimsNamesTheCoveringDaemon(t *testing.T) {
	got := uncoveredStoreClaims([]string{"tsk_a", "tsk_b"})
	for _, want := range []string{
		"tsk_a, tsk_b",
		"`lore task heartbeat --daemon --id tsk_a`",
		"`lore task heartbeat --daemon --id tsk_b`",
		"not renewed by it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("devco:D-673: the start notice lacks %q:\n%s", want, got)
		}
	}
}
