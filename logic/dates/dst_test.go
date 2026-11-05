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
// moves forward by the gap: TestLocalTimeAcrossTransitions (PC-TASK-3).
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

// TestLocalTimeAcrossTransitions (PC-TASK-3): "<day> HH:MM" across zones
// on both clock-change days and an ordinary day. A wall time inside a
// spring-forward gap moves forward by the gap length (1 h in New York and
// Berlin, 30 min on Lord Howe); one a fall-back repeats takes the earlier
// offset (the first instant); every other time reads plainly. The zone is
// always injected, never the machine's.
func TestLocalTimeAcrossTransitions(t *testing.T) {
	zone := func(name string) *time.Location {
		l, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	ny, berlin, lh := zone("America/New_York"), zone("Europe/Berlin"), zone("Australia/Lord_Howe")
	cases := []struct {
		name  string
		loc   *time.Location
		day   string // YYYY-MM-DD, local
		clock string
		want  string // the UTC instant
		local string // what the instant reads as on loc's wall clock
		note  bool   // a gap note is given
	}{
		// New York: spring 2026-03-08 02:00 EST → 03:00 EDT; fall 2026-11-01 02:00 EDT → 01:00 EST.
		{"ny spring before gap", ny, "2026-03-08", "01:59", "2026-03-08T06:59:00Z", "01:59", false},
		{"ny spring gap start", ny, "2026-03-08", "02:00", "2026-03-08T07:00:00Z", "03:00", true},
		{"ny spring gap 02:30", ny, "2026-03-08", "02:30", "2026-03-08T07:30:00Z", "03:30", true},
		{"ny spring gap end", ny, "2026-03-08", "02:59", "2026-03-08T07:59:00Z", "03:59", true},
		{"ny spring after gap", ny, "2026-03-08", "03:00", "2026-03-08T07:00:00Z", "03:00", false},
		{"ny fall before repeat", ny, "2026-11-01", "00:59", "2026-11-01T04:59:00Z", "00:59", false},
		{"ny fall repeat start", ny, "2026-11-01", "01:00", "2026-11-01T05:00:00Z", "01:00", false},
		{"ny fall repeat 01:30 earlier offset", ny, "2026-11-01", "01:30", "2026-11-01T05:30:00Z", "01:30", false},
		{"ny fall after repeat", ny, "2026-11-01", "02:00", "2026-11-01T07:00:00Z", "02:00", false},
		{"ny ordinary", ny, "2026-06-15", "02:30", "2026-06-15T06:30:00Z", "02:30", false},
		// Berlin: spring 2026-03-29 02:00 CET → 03:00 CEST; fall 2026-10-25 03:00 CEST → 02:00 CET.
		{"berlin spring gap 02:30", berlin, "2026-03-29", "02:30", "2026-03-29T01:30:00Z", "03:30", true},
		{"berlin spring after gap", berlin, "2026-03-29", "03:30", "2026-03-29T01:30:00Z", "03:30", false},
		{"berlin fall repeat 02:30 earlier offset", berlin, "2026-10-25", "02:30", "2026-10-25T00:30:00Z", "02:30", false},
		{"berlin fall after repeat", berlin, "2026-10-25", "03:00", "2026-10-25T02:00:00Z", "03:00", false},
		{"berlin ordinary", berlin, "2026-01-15", "02:30", "2026-01-15T01:30:00Z", "02:30", false},
		// Lord Howe (30-minute change): spring 2026-10-04 02:00 +10:30 → 02:30 +11; fall 2026-04-05 02:00 +11 → 01:30 +10:30.
		{"lh spring gap 02:15 moves 30 min", lh, "2026-10-04", "02:15", "2026-10-03T15:45:00Z", "02:45", true},
		{"lh spring gap start", lh, "2026-10-04", "02:00", "2026-10-03T15:30:00Z", "02:30", true},
		{"lh spring after gap", lh, "2026-10-04", "02:30", "2026-10-03T15:30:00Z", "02:30", false},
		{"lh fall repeat 01:45 earlier offset", lh, "2026-04-05", "01:45", "2026-04-04T14:45:00Z", "01:45", false},
		{"lh fall after repeat", lh, "2026-04-05", "02:00", "2026-04-04T15:30:00Z", "02:00", false},
		{"lh ordinary", lh, "2026-07-01", "02:15", "2026-06-30T15:45:00Z", "02:15", false},
		// UTC never shifts.
		{"utc ny spring day", time.UTC, "2026-03-08", "02:30", "2026-03-08T02:30:00Z", "02:30", false},
		{"utc ordinary", time.UTC, "2026-06-15", "23:59", "2026-06-15T23:59:00Z", "23:59", false},
	}
	for _, c := range cases {
		day, err := time.ParseInLocation(DayLayout, c.day, c.loc)
		if err != nil {
			t.Fatal(err)
		}
		// "now" is noon of the day in loc; "today HH:MM" and the explicit
		// "YYYY-MM-DD HH:MM" must agree.
		now := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, c.loc)
		for _, phrase := range []string{"today " + c.clock, c.day + " " + c.clock} {
			r, perr := Parse(phrase, Clock{Now: now.UTC(), Loc: c.loc, WeekEnd: time.Friday})
			if perr != nil {
				t.Errorf("%s: %q: %v", c.name, phrase, perr)
				continue
			}
			if got := r.Text(); got != c.want {
				t.Errorf("%s: %q = %s, want %s", c.name, phrase, got, c.want)
			}
			lt := r.At.In(c.loc)
			if got := lt.Format("15:04"); got != c.local || lt.Format(DayLayout) != c.day {
				t.Errorf("%s: %q reads %s on the wall clock, want %s %s", c.name, phrase, lt.Format("2006-01-02 15:04"), c.day, c.local)
			}
			if (r.Note != "") != c.note {
				t.Errorf("%s: %q note %q, want a gap note: %v", c.name, phrase, r.Note, c.note)
			}
		}
	}
}
