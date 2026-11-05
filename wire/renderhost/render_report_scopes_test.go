package renderhost

import "testing"

// TestScopeStatus pins the 6.2.6 statuses: over the budget, past warn_at, ok,
// and no budget at all.
func TestScopeStatus(t *testing.T) {
	cases := []struct {
		bytes, budget, warn int
		want                string
	}{
		{9000, 8000, 80, scopeStatusOver},
		{7000, 8000, 80, scopeStatusWarn},
		{6400, 8000, 80, scopeStatusOK},
		{99999, 0, 80, scopeStatusOK},
	}
	for _, c := range cases {
		if got := scopeStatus(c.bytes, c.budget, c.warn); got != c.want {
			t.Errorf("scopeStatus(%d,%d,%d) = %q, want %q", c.bytes, c.budget, c.warn, got, c.want)
		}
	}
}

// TestHasSubGroup: only a folder with no folder file under it ends a chain.
func TestHasSubGroup(t *testing.T) {
	paths := []string{"src/booking", "src/booking/payments", "src/bookingx"}
	if !hasSubGroup(paths, "src/booking") {
		t.Error("src/booking has src/booking/payments under it")
	}
	if hasSubGroup(paths, "src/booking/payments") || hasSubGroup(paths, "src/bookingx") {
		t.Error("leaf folders have nothing under them")
	}
}
