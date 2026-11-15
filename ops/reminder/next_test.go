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
		got, skipped, ok := nextOccurrence(at(tc.due), tc.rec, at(tc.now), time.UTC)
		if !ok || !got.Equal(at(tc.want)) || skipped != tc.skipped {
			t.Errorf("%s: got %s skipped %d ok %v, want %s skipped %d", tc.name, got.Format(time.RFC3339), skipped, ok, tc.want, tc.skipped)
		}
	}
	if _, _, ok := nextOccurrence(at("2026-01-01T00:00:00Z"), "2w", at("2026-02-01T00:00:00Z"), time.UTC); ok {
		t.Error("an unknown pattern is refused")
	}
}

// TestNextOccurrenceKeepsWallClockAcrossDST (devco:T-8): a weekly 09:00
// reminder is 09:00 local again after the DST change in New York and
// Berlin (not 7×24 h later in UTC, which slid it to 10:00 or 08:00); a
// date-only due still steps UTC days; years late, k never overshoots.
func TestNextOccurrenceKeepsWallClockAcrossDST(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	cases := []struct {
		name      string
		loc       *time.Location
		due, now  time.Time
		rec, want string
	}{
		{"NY spring forward", ny, time.Date(2027, 3, 13, 9, 0, 0, 0, ny), time.Date(2027, 3, 13, 10, 0, 0, 0, ny), schema.Recurrence7d, "2027-03-20T13:00:00Z"},
		{"NY fall back", ny, time.Date(2027, 10, 31, 9, 0, 0, 0, ny), time.Date(2027, 10, 31, 10, 0, 0, 0, ny), schema.Recurrence7d, "2027-11-07T14:00:00Z"},
		{"Berlin spring forward", berlin, time.Date(2027, 3, 24, 9, 0, 0, 0, berlin), time.Date(2027, 3, 24, 10, 0, 0, 0, berlin), schema.Recurrence7d, "2027-03-31T07:00:00Z"},
		{"Berlin monthly across the change", berlin, time.Date(2027, 2, 28, 9, 0, 0, 0, berlin), time.Date(2027, 2, 28, 10, 0, 0, 0, berlin), schema.Recurrence1m, "2027-03-28T07:00:00Z"},
		{"NY years late lands on 09:00", ny, time.Date(2020, 1, 4, 9, 0, 0, 0, ny), time.Date(2027, 3, 14, 12, 0, 0, 0, ny), schema.Recurrence7d, "2027-03-20T13:00:00Z"},
		{"date-only due steps UTC days", ny, time.Date(2027, 3, 13, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 13, 1, 0, 0, 0, time.UTC), schema.Recurrence7d, "2027-03-20T00:00:00Z"},
	}
	for _, tc := range cases {
		got, _, ok := nextOccurrence(tc.due.UTC(), tc.rec, tc.now, tc.loc)
		if !ok || got.UTC().Format(time.RFC3339) != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got.UTC().Format(time.RFC3339), tc.want)
		}
	}
}
