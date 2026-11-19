package directive

import (
	"strings"
	"testing"
)

// TestDirectiveTakesWorkWithAClaim (devco:S-18): LORE.md's directive told
// an agent to `lore task start --id <id>`, which does not claim, so a
// second agent's `lore task next` handed out the same task. Both forms now
// name task next --claim / task claim with --push, the heartbeat and done
// --commit, and say a bare task start does not claim.
func TestDirectiveTakesWorkWithAClaim(t *testing.T) {
	for _, full := range []bool{false, true} {
		b := Block(full)
		for _, want := range []string{"lore task next --claim --push", "lore task claim --id", "lore task heartbeat --id", "--commit <sha>", "does not claim"} {
			if !strings.Contains(b, want) {
				t.Errorf("directive (full=%v) misses %q", full, want)
			}
		}
		for _, bad := range []string{"`lore task start --id <id>` / ", "Mark `task start <tsk_id>` when you begin"} {
			if strings.Contains(b, bad) {
				t.Errorf("directive (full=%v) still starts work without a claim: %q", full, bad)
			}
		}
	}
}
