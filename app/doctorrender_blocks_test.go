package app

import (
	"strings"
	"testing"
)

// TestWithContextBlocksSplitsWarnings: a size line under the warn level is
// information in its own block, never listed right under doctor's
// Warnings: as one more bullet (bug hunt G-9).
func TestWithContextBlocksSplitsWarnings(t *testing.T) {
	doctor := "Warnings:\n  - live: the remote live ref was rewritten\n"
	info := "context: .lore/LORE.md 2668 of 32000 bytes (8%, profile claude_code); warn at 80%"
	got := withContextBlocks(doctor, nil, []string{info})
	if strings.Contains(got, "Warnings:\n  - live: the remote live ref was rewritten\n\nContext:\n") {
		t.Fatalf("information listed as a bare Context: block after Warnings:\n%s", got)
	}
	if !strings.Contains(got, "\n"+contextInfoHead+"\n  - "+info+"\n") || strings.Contains(got, contextWarnHead) {
		t.Fatalf("information not in its own block:\n%s", got)
	}
	over := "context: .lore/LORE.md 38662 of 32000 bytes (120%)"
	got = withContextBlocks(doctor, []string{over}, []string{info})
	wi, ii := strings.Index(got, contextWarnHead+"\n  WARN "+over), strings.Index(got, contextInfoHead)
	if wi < 0 || ii < wi {
		t.Fatalf("warnings block missing or after the information block:\n%s", got)
	}
}
