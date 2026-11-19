//go:build stories && !windows

package stories

import "testing"

// TestWWNormalizeIgnoresNamePadding: the twins print the same doctor line
// for stores whose names differ in length; the column padding after the
// name follows its length (doctor pads an area to 12, then 3 spaces), so
// the twin rule must read both as one shape and still tell real wording
// apart (D-687).
func TestWWNormalizeIgnoresNamePadding(t *testing.T) {
	ops := "setup " + wwOpsSetup + "\n  ✓ store " + wwOpsOrg + "   read ✓ write ✓ private ✓\n  ✓ tools       claude_code"
	co := "setup " + wSetup + "\n  ✓ store " + wOrg + "  read ✓ write ✓ private ✓\n  ✓ tools       claude_code"
	if a, b := wwNormalize(ops), wwNormalize(co); a != b {
		t.Fatalf("twins differ by name padding only, want one shape:\n- %q\n+ %q", a, b)
	}
	other := "setup " + wSetup + "\n  ✓ store " + wOrg + "  read ✓ write ✗ private ✓\n  ✓ tools       claude_code"
	if wwNormalize(ops) == wwNormalize(other) {
		t.Fatal("a real difference (write ✗) was normalized away")
	}
	// the indent is shape, not padding: a nested line stays nested
	if got := wwNormalize("a\n    fix: x"); got != "a\n    fix: x" {
		t.Fatalf("indent changed: %q", got)
	}
}
