package wire

import "testing"

// TestHandleKeyFoldsAccents pins I-10: @chloé, @Chloe and chloe resolve
// to the one stored handle.
func TestHandleKeyFoldsAccents(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"@chloé", "@Chloe", "chloe", " @CHLOÉ "} {
		if got := handleKey(in); got != "chloe" {
			t.Errorf("handleKey(%q) = %q, want chloe", in, got)
		}
	}
}
