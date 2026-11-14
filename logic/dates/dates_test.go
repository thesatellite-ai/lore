package dates

import (
	"errors"
	"testing"
	"time"
)

// TestTable is TASK 12.1's table, row by row (today Wed 2026-11-04).
func TestTable(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	c := Clock{Now: now, Loc: time.UTC, WeekEnd: time.Friday}
	for in, want := range map[string]string{
		"today": "2026-11-04", "tomorrow": "2026-11-05", "yesterday": "2026-11-03",
		"fri": "2026-11-06", "wed": "2026-11-11", "mon": "2026-11-09", "next fri": "2026-11-13", "this fri": "2026-11-06",
		"this wed": "2026-11-04", "next mon": "2026-11-09",
		"in 3d": "2026-11-07", "in 2w": "2026-11-18", "in 1m": "2026-12-04", "in 3 days": "2026-11-07", "in 1y": "2027-11-04",
		"eow": "2026-11-06", "end of week": "2026-11-06", "eom": "2026-11-30", "end of month": "2026-11-30",
		"nov 20": "2026-11-20", "20 nov": "2026-11-20", "2026-11-20": "2026-11-20", "Nov  20": "2026-11-20",
		"nov 2":          "2027-11-02",
		"tomorrow 17:00": "2026-11-05T17:00:00Z", "2026-11-20T08:00:00+05:30": "2026-11-20T02:30:00Z",
	} {
		r, err := Parse(in, c)
		if err != nil || r.Text() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, r.Text(), err, want)
		}
	}
	for _, in := range []string{"", "11/12", "11/12/2026", "next month", "this mon", "feb 30", "soon", "in 3x", "next foo", "25:00"} {
		_, err := Parse(in, c)
		var de *Error
		if !errors.As(err, &de) {
			t.Errorf("Parse(%q) = %v; want *Error", in, err)
		}
	}
}

// TestZoneDay: the local day decides "today"; a clock time is local,
// stored UTC.
func TestZoneDay(t *testing.T) {
	west := time.FixedZone("UTC-8", -8*3600)
	now := time.Date(2026, 12, 2, 4, 30, 0, 0, time.UTC) // 20:30 on 12-01 in UTC-8
	r, err := Parse("today", Clock{Now: now, Loc: west})
	if err != nil || r.Text() != "2026-12-01" {
		t.Fatalf("today = %q %v", r.Text(), err)
	}
	r, err = Parse("tomorrow 09:30", Clock{Now: now, Loc: west})
	if err != nil || r.Text() != "2026-12-02T17:30:00Z" {
		t.Fatalf("tomorrow 09:30 = %q %v", r.Text(), err)
	}
}

// TestAddMonths clamps month ends and leap years.
func TestAddMonths(t *testing.T) {
	for _, c := range []struct {
		in, want string
		n        int
	}{
		{"2026-01-31", "2026-02-28", 1}, {"2028-01-31", "2028-02-29", 1}, {"2026-03-31", "2026-04-30", 1}, {"2026-12-15", "2027-01-15", 1}, {"2028-02-29", "2029-02-28", 12},
	} {
		d, _ := time.Parse(DayLayout, c.in)
		if got := AddMonths(d, c.n).Format(DayLayout); got != c.want {
			t.Errorf("AddMonths(%s,%d) = %s want %s", c.in, c.n, got, c.want)
		}
	}
}

// TestDayInAndDueBy pins I-8: a clock time is listed as its day in the
// viewer's zone, a calendar day stays that day, and a day is due once it
// has begun where the viewer is (not at UTC midnight).
func TestDayInAndDueBy(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database")
	}
	sat2000LA := time.Date(2026, 11, 15, 4, 0, 0, 0, time.UTC) // Sat 11-14 20:00 PST
	if got := DayIn(sat2000LA, la); got != "2026-11-14" {
		t.Errorf("DayIn instant = %s", got)
	}
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if got := DayIn(day, la); got != "2026-11-14" {
		t.Errorf("DayIn day = %s", got)
	}
	fri1900LA := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	if DueBy(day, fri1900LA, la) {
		t.Error("a 11-14 day is due on Fri 11-13 in LA")
	}
	if !DueBy(day, fri1900LA, time.UTC) {
		t.Error("a 11-14 day is not due at 03:00 UTC on 11-14 in UTC")
	}
	if DueBy(sat2000LA, fri1900LA, la) || !DueBy(sat2000LA, sat2000LA.Add(time.Minute), la) {
		t.Error("instant due compare")
	}
	r, err := Parse("tomorrow + 09:00", Clock{Now: fri1900LA, Loc: la, WeekEnd: time.Friday})
	if err != nil || r.Day || r.At.In(la).Format("2006-01-02 15:04") != "2026-11-14 09:00" {
		t.Errorf("tomorrow + 09:00 = %v, %v", r, err)
	}
}
