package dates

import (
	"testing"
	"time"
	_ "time/tzdata" // a fixed zone database, whatever the test machine has
)

// TestPhrasesAcrossDST (OV15b, TASK 12, TSQ11): on and around the days a
// zone changes its clocks, a phrase's day is the local calendar day (never
// a 24-hour step that lands on the wrong day), stored as midnight UTC; a
// clock time is read in the local zone of that day (its own offset, not
// today's) and stored as the UTC instant; one in the repeated autumn hour
// takes the first (summer-time) instant. A time inside the spring gap
// (02:30 on the change day) does not exist: TestPhraseInSpringGap pins
// only that it lands on the change day next to the gap.
func TestPhrasesAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	utc := func(s string) time.Time {
		v, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			t.Fatal(perr)
		}
		return v
	}
	cases := []struct {
		name, now string
		loc       *time.Location
		phrase    string
		want      string // Result.Text()
	}{
		// US spring forward: 2026-03-08 02:00 EST → 03:00 EDT (07:00Z).
		{"spring today before change", "2026-03-08T06:30:00Z", ny, "today", "2026-03-08"},
		{"spring local day still yesterday", "2026-03-08T04:59:00Z", ny, "today", "2026-03-07"},
		{"spring tomorrow", "2026-03-08T06:30:00Z", ny, "tomorrow", "2026-03-09"},
		{"spring in 1d", "2026-03-07T12:00:00Z", ny, "in 1d", "2026-03-08"},
		{"spring in 1w is a calendar week", "2026-03-05T17:00:00Z", ny, "in 1w", "2026-03-12"},
		{"spring before gap, winter offset", "2026-03-08T05:00:00Z", ny, "today 01:30", "2026-03-08T06:30:00Z"},
		{"spring after gap, summer offset", "2026-03-08T05:00:00Z", ny, "today 03:30", "2026-03-08T07:30:00Z"},
		{"next day uses its own offset", "2026-03-07T15:00:00Z", ny, "tomorrow 09:00", "2026-03-08T13:00:00Z"},
		// US fall back: 2026-11-01 02:00 EDT → 01:00 EST (06:00Z).
		{"fall today", "2026-11-01T05:30:00Z", ny, "today", "2026-11-01"},
		{"fall repeated hour first instant", "2026-11-01T05:10:00Z", ny, "today 01:30", "2026-11-01T05:30:00Z"},
		{"fall tomorrow winter offset", "2026-11-01T12:00:00Z", ny, "tomorrow 09:00", "2026-11-02T14:00:00Z"},
		{"fall in 1d late evening", "2026-11-02T04:30:00Z", ny, "in 1d", "2026-11-02"},
		// Europe: 2026-03-29 02:00 CET → 03:00 CEST (01:00Z).
		{"eu spring eow", "2026-03-29T00:30:00Z", berlin, "eow", "2026-04-03"},
		{"eu spring next mon", "2026-03-28T23:30:00Z", berlin, "next mon", "2026-03-30"},
		{"eu spring eom", "2026-03-29T00:30:00Z", berlin, "eom", "2026-03-31"},
		{"eu spring in 1m", "2026-03-29T00:30:00Z", berlin, "in 1m", "2026-04-29"},
	}
	for _, c := range cases {
		r, perr := Parse(c.phrase, Clock{Now: utc(c.now), Loc: c.loc, WeekEnd: time.Friday})
		if perr != nil {
			t.Errorf("%s: %v", c.name, perr)
			continue
		}
		if got := r.Text(); got != c.want {
			t.Errorf("%s: %q at %s in %s = %s, want %s", c.name, c.phrase, c.now, c.loc, got, c.want)
		}
		if r.Day && !r.At.Equal(r.At.Truncate(24*time.Hour)) {
			t.Errorf("%s: a day is not midnight UTC: %s", c.name, r.At)
		}
	}
}

// TestPhraseInSpringGap: "today 02:30" on the US spring-forward day names
// a local time that does not exist. Go's time.Date picks one side of the
// gap (today 06:30Z, i.e. 01:30 EST); the design does not say which, so
// this pins only that the instant is within an hour of the gap and on the
// change day, never a different day.
func TestPhraseInSpringGap(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)
	r, err := Parse("today 02:30", Clock{Now: now, Loc: ny, WeekEnd: time.Friday})
	if err != nil {
		t.Fatal(err)
	}
	gap := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	if d := r.At.Sub(gap); d < -time.Hour || d > time.Hour || r.Day {
		t.Errorf("gap time = %s, want within an hour of %s", r.At, gap)
	}
}
