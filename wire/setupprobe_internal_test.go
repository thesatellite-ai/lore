package wire

import (
	"regexp"
	"strings"
	"testing"
)

// TestProbeRefToken (A13 bug 2): the write test's ref token fits the
// probe's 1-64 [A-Za-z0-9_-] rule for a real machine id (mch_ + 64 hex =
// 68 characters), keeps its prefix, and is never empty.
func TestProbeRefToken(t *testing.T) {
	t.Parallel()
	ok := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	real := "mch_" + strings.Repeat("0123456789abcdef", 4)
	for _, in := range []string{real, "mch_ab/cd ef", "", "///"} {
		got := probeRefToken(in)
		if !ok.MatchString(got) {
			t.Fatalf("probeRefToken(%q) = %q, not a valid probe ref token", in, got)
		}
	}
	if got := probeRefToken(real); !strings.HasPrefix(real, got) || len(got) != probeRefTokenMax {
		t.Fatalf("probeRefToken(real) = %q", got)
	}
}
