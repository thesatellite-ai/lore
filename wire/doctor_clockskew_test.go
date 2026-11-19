package wire

import (
	"strings"
	"testing"
	"time"
)

// TestClockSkewFinding (devco:U-3): a store commit stamped more than
// clockSkewLimit ahead of this clock is a warning naming the store, the gap
// and the author; ordinary drift and old commits say nothing. Before, the
// docs promised doctor checked clock skew and no doctor did.
func TestClockSkewFinding(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if _, skewed := clockSkewFinding("acme/org", now, now.Add(4*time.Minute), "Kai"); skewed {
		t.Fatal("4 minutes ahead is ordinary drift, not a finding")
	}
	if _, skewed := clockSkewFinding("acme/org", now, now.Add(-48*time.Hour), "Kai"); skewed {
		t.Fatal("an old commit is not a skew")
	}
	f, skewed := clockSkewFinding("acme/org", now, now.Add(2*time.Hour), "Kai Ahead")
	if !skewed || f.Level != DoctorWarning || f.Check != DoctorCheckClockSkew {
		t.Fatalf("2 h ahead: %+v (skewed %v), want a clock_skew warning", f, skewed)
	}
	for _, want := range []string{"store acme/org", "2h0m0s ahead of this machine's clock", "by Kai Ahead"} {
		if !strings.Contains(f.Message, want) {
			t.Fatalf("message %q lacks %q", f.Message, want)
		}
	}
	if !strings.Contains(f.Fix, "date -u") {
		t.Fatalf("fix %q names no way to compare the clocks", f.Fix)
	}
}
