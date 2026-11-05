package trash

import (
	"testing"
	"time"
)

// TestSinceOf is `trash purge --archived-before` / `--older-than`'s cutoff
// (B11 port of saas/cmd/cli sync_wire_test.go TestParseCutoff; `lore sync
// purge` is the old spelling, FA-DEL-4): a date is that day at local
// midnight, an age (days or a Go duration) counts back from now, and a
// negative or unparseable value is refused. Empty means "no cutoff"; the
// op never gets there (purge needs exactly one mode).
func TestSinceOf(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Time
	}{
		{"2026-01-31", time.Date(2026, 1, 31, 0, 0, 0, 0, time.Local)},
		{" 2026-01-31 ", time.Date(2026, 1, 31, 0, 0, 0, 0, time.Local)},
		{"90d", now.Add(-90 * 24 * time.Hour)},
		{"720h", now.Add(-720 * time.Hour)},
		{"36h30m", now.Add(-36*time.Hour - 30*time.Minute)},
		{"0d", now},
		{"", time.Time{}},
	} {
		got, err := sinceOf(tc.in, now)
		if err != nil || !got.Equal(tc.want) {
			t.Errorf("%q: got %v err %v, want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"x", "-5d", "abcd", "-1h", "5 d", "2026-13-01", "d"} {
		if _, err := sinceOf(bad, now); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
