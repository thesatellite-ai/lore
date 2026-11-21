package dates

import (
	"errors"
	"testing"
	"time"
)

// TestRelativePast (H-63): the past phrases a person asks about ("what
// happened 2 days ago", "since last week", "2 fridays ago") read against a
// pinned now; they were E_DATE ("not in the table") before.
func TestRelativePast(t *testing.T) {
	wed := Clock{Now: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), Loc: time.UTC, WeekEnd: time.Friday} // Wed 2026-11-04
	fri := Clock{Now: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC), Loc: time.UTC, WeekEnd: time.Friday} // Fri 2026-11-06
	for _, tc := range []struct {
		c         Clock
		in        string
		want      string
		wantLast  string
		wantSpan  bool
		wantNoted bool
	}{
		{c: wed, in: "2 days ago", want: "2026-11-02"},
		{c: wed, in: "1 day ago", want: "2026-11-03"},
		{c: wed, in: "a day ago", want: "2026-11-03"},
		{c: wed, in: "3d ago", want: "2026-11-01"},
		{c: wed, in: "2 weeks ago", want: "2026-10-21"},
		{c: wed, in: "a week ago", want: "2026-10-28"},
		{c: wed, in: "1w ago", want: "2026-10-28"},
		{c: wed, in: "2 months ago", want: "2026-09-04"},
		{c: wed, in: "a year ago", want: "2025-11-04"},
		{c: wed, in: "0 days ago", want: "2026-11-04"},
		{c: wed, in: "last fri", want: "2026-10-30"},
		{c: wed, in: "last friday", want: "2026-10-30"},
		{c: wed, in: "last tue", want: "2026-11-03"},
		{c: wed, in: "last wed", want: "2026-10-28"},
		{c: wed, in: "2 fridays ago", want: "2026-10-23"},
		{c: wed, in: "1 friday ago", want: "2026-10-30"},
		{c: wed, in: "a monday ago", want: "2026-11-02"},
		{c: wed, in: "3 mons ago", want: "2026-10-19"},
		{c: wed, in: "2 Fridays  Ago", want: "2026-10-23"},
		{c: fri, in: "last fri", want: "2026-10-30"},
		{c: fri, in: "2 fridays ago", want: "2026-10-23"},
		{c: wed, in: "2 days ago 09:00", want: "2026-11-02T09:00:00Z"},
		{c: wed, in: "last week", want: "2026-10-26", wantLast: "2026-11-01", wantSpan: true},
		{c: wed, in: "this week", want: "2026-11-02", wantLast: "2026-11-08", wantSpan: true},
		{c: wed, in: "last month", want: "2026-10-01", wantLast: "2026-10-31", wantSpan: true},
		{c: wed, in: "this month", want: "2026-11-01", wantLast: "2026-11-30", wantSpan: true},
		{c: Clock{Now: time.Date(2027, 1, 13, 0, 0, 0, 0, time.UTC), Loc: time.UTC}, in: "last month", want: "2026-12-01", wantLast: "2026-12-31", wantSpan: true},
		{c: Clock{Now: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), Loc: time.UTC}, in: "1 month ago", want: "2026-02-28"},
	} {
		r, err := Parse(tc.in, tc.c)
		if err != nil || r.Text() != tc.want {
			t.Errorf("Parse(%q) = %q, %v; want %q", tc.in, r.Text(), err, tc.want)
			continue
		}
		if got := !r.Last.IsZero(); got != tc.wantSpan {
			t.Errorf("Parse(%q) span = %v; want %v", tc.in, got, tc.wantSpan)
		}
		if tc.wantSpan && (r.LastDay().Format(DayLayout) != tc.wantLast || r.Note == "") {
			t.Errorf("Parse(%q) last = %s note %q; want %s and a note", tc.in, r.LastDay().Format(DayLayout), r.Note, tc.wantLast)
		}
		if !tc.wantSpan && r.LastDay() != r.At {
			t.Errorf("Parse(%q) LastDay %v != At %v", tc.in, r.LastDay(), r.At)
		}
	}
	for _, in := range []string{"0 fridays ago", "last foo", "last week 09:00", "2 foos ago", "ago", "days ago", "-2 days ago", "2 days", "last", "next week", "next month"} {
		_, err := Parse(in, wed)
		var de *Error
		if !errors.As(err, &de) {
			t.Errorf("Parse(%q) = %v; want *Error", in, err)
		}
	}
}

// TestWindowSpan (H-63): a span phrase as --until keeps its last day, so
// --since "last week" --until "last week" is the whole of last week.
func TestWindowSpan(t *testing.T) {
	c := Clock{Now: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), Loc: time.UTC}
	r, err := Parse("last week", c)
	if err != nil {
		t.Fatal(err)
	}
	w := WindowOf(&r, &r)
	for at, keep := range map[string]bool{
		"2026-10-25T23:59:59Z": false, "2026-10-26T00:00:00Z": true, "2026-10-30T12:00:00Z": true,
		"2026-11-01T23:59:59Z": true, "2026-11-02T00:00:00Z": false,
	} {
		ts, _ := time.Parse(time.RFC3339, at)
		if w.Keeps(ts) != keep {
			t.Errorf("last week keeps %s = %v; want %v", at, !keep, keep)
		}
	}
}
