package reminder

// The recurrence step matrix (tasks area): every pattern from ordinary
// days and from month ends, so a monthly reminder never skips a month
// (Jan 31 + 1m is Feb 28) and day steps stay plain day counts.

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/schema"
)

// TestTasksReminderBumpMatrix checks bumpRecurrence pattern × start day.
func TestTasksReminderBumpMatrix(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cases := []struct{ from, rec, want string }{
		{"2026-11-05", schema.Recurrence7d, "2026-11-12"},
		{"2026-12-28", schema.Recurrence7d, "2027-01-04"},
		{"2026-01-31", schema.Recurrence30d, "2026-03-02"},
		{"2026-11-05", schema.Recurrence1m, "2026-12-05"},
		{"2026-01-31", schema.Recurrence1m, "2026-02-28"},
		{"2028-01-31", schema.Recurrence1m, "2028-02-29"},
		{"2026-03-31", schema.Recurrence1m, "2026-04-30"},
		{"2026-12-31", schema.Recurrence1m, "2027-01-31"},
		{"2026-11-30", schema.Recurrence3m, "2027-02-28"},
		{"2026-08-31", schema.Recurrence6m, "2027-02-28"},
		{"2028-02-29", schema.Recurrence1y, "2029-02-28"},
		{"2026-06-15", schema.Recurrence1y, "2027-06-15"},
	}
	for _, tc := range cases {
		got, ok := bumpRecurrence(day(tc.from), tc.rec)
		if !ok || got.Format(time.DateOnly) != tc.want {
			t.Errorf("%s + %s = %s (ok %v), want %s", tc.from, tc.rec, got.Format(time.DateOnly), ok, tc.want)
		}
	}
	if _, ok := bumpRecurrence(day("2026-11-05"), "2w"); ok {
		t.Fatal("a pattern outside the set is not ok")
	}
	at := time.Date(2026, 1, 31, 14, 30, 0, 0, time.UTC)
	if got, _ := bumpRecurrence(at, schema.Recurrence1m); !got.Equal(time.Date(2026, 2, 28, 14, 30, 0, 0, time.UTC)) {
		t.Fatalf("the time of day is kept: %s", got)
	}
}
