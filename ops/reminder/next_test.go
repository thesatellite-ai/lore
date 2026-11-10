package reminder

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/schema"
)

// TestNextOccurrence (INTEG-TRASH-5): done before its date moves one
// period; done after it moves to the first occurrence after now and counts
// the skipped ones; month ends never drift; a date exactly now is past.
func TestNextOccurrence(t *testing.T) {
	t.Parallel()
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name, due, rec, now, want string
		skipped                   int
	}{
		{"early 7d", "2026-10-01T09:00:00Z", schema.Recurrence7d, "2026-09-20T00:00:00Z", "2026-10-08T09:00:00Z", 0},
		{"on time 7d", "2026-10-01T09:00:00Z", schema.Recurrence7d, "2026-10-01T10:00:00Z", "2026-10-08T09:00:00Z", 0},
		{"late 7d (the bug)", "2026-10-01T00:00:00Z", schema.Recurrence7d, "2026-11-06T12:00:00Z", "2026-11-12T00:00:00Z", 5},
		{"exactly now is past", "2026-10-01T00:00:00Z", schema.Recurrence7d, "2026-10-08T00:00:00Z", "2026-10-15T00:00:00Z", 1},
		{"late 30d", "2026-01-01T00:00:00Z", schema.Recurrence30d, "2026-03-15T00:00:00Z", "2026-04-01T00:00:00Z", 2},
		{"years late 7d", "2020-01-01T00:00:00Z", schema.Recurrence7d, "2026-11-07T00:00:00Z", "2026-11-11T00:00:00Z", 357},
		{"late 1m month end, no drift", "2026-01-31T00:00:00Z", schema.Recurrence1m, "2026-04-02T00:00:00Z", "2026-04-30T00:00:00Z", 2},
		{"late 1m keeps the 31st", "2026-01-31T00:00:00Z", schema.Recurrence1m, "2026-05-01T00:00:00Z", "2026-05-31T00:00:00Z", 3},
		{"late 3m", "2026-01-15T00:00:00Z", schema.Recurrence3m, "2026-11-07T00:00:00Z", "2027-01-15T00:00:00Z", 3},
		{"late 6m", "2025-08-31T00:00:00Z", schema.Recurrence6m, "2026-11-07T00:00:00Z", "2027-02-28T00:00:00Z", 2},
		{"late 1y leap day", "2024-02-29T00:00:00Z", schema.Recurrence1y, "2026-11-07T00:00:00Z", "2027-02-28T00:00:00Z", 2},
		{"years late 1m", "2016-03-31T00:00:00Z", schema.Recurrence1m, "2026-11-07T00:00:00Z", "2026-11-30T00:00:00Z", 127},
	}
	for _, tc := range cases {
		got, skipped, ok := nextOccurrence(at(tc.due), tc.rec, at(tc.now))
		if !ok || !got.Equal(at(tc.want)) || skipped != tc.skipped {
			t.Errorf("%s: got %s skipped %d ok %v, want %s skipped %d", tc.name, got.Format(time.RFC3339), skipped, ok, tc.want, tc.skipped)
		}
	}
	if _, _, ok := nextOccurrence(at("2026-01-01T00:00:00Z"), "2w", at("2026-02-01T00:00:00Z")); ok {
		t.Error("an unknown pattern is refused")
	}
}
