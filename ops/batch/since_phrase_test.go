package batch

import (
	"testing"
	"time"
)

// TestSinceOfPhrase (H-63): batch list --since reads the date phrases of
// every other --since ("2 days ago", "last week"), a day at local
// midnight, next to its own durations; anything else is still refused.
func TestSinceOfPhrase(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local) // Sat
	for in, want := range map[string]time.Time{
		"2 days ago": time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
		"last week":  time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local),
		"last wed":   time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local),
		"7d":         now.Add(-7 * 24 * time.Hour),
	} {
		got, err := sinceOf(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: got %v err %v, want %v", in, got, err, want)
		}
	}
	if _, err := sinceOf("soonish", now); err == nil {
		t.Error("soonish must be refused")
	}
}
