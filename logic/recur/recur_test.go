package recur

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func untilISO(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

// TestNextTable: TASK 10.2 forms across month ends and leap years.
func TestNextTable(t *testing.T) {
	for _, c := range []struct{ rule, base, want string }{
		{"every day", "2026-12-31", "2027-01-01"},
		{"every weekday", "2026-11-06", "2026-11-09"},
		{"every week", "2026-11-04", "2026-11-11"},
		{"every 2 weeks", "2026-11-04", "2026-11-18"},
		{"every 3 days", "2028-02-28", "2028-03-02"},
		{"every month", "2026-01-31", "2026-02-28"},
		{"every quarter", "2026-11-30", "2027-02-28"},
		{"every year", "2028-02-29", "2029-02-28"},
		{"every mon,thu", "2026-11-04", "2026-11-05"},
		{"every mon", "2026-11-09", "2026-11-16"},
		{"every month on 1", "2026-11-04", "2026-12-01"},
		{"every month on 31", "2027-01-31", "2027-02-28"},
		{"every month on last", "2026-11-30", "2026-12-31"},
		{"every month on last", "2028-02-10", "2028-02-29"},
		{"every! 3 days", "2026-12-02", "2026-12-05"},
		{"Every  2 Months", "2026-12-31", "2027-02-28"},
	} {
		r, err := Parse(c.rule, untilISO)
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		if got := r.Next(day(c.base)).Format("2006-01-02"); got != c.want {
			t.Errorf("%s after %s = %s, want %s", c.rule, c.base, got, c.want)
		}
	}
}

// TestRefused: everything outside the table is *Error.
func TestRefused(t *testing.T) {
	for _, in := range []string{"", "weekly", "every", "every fortnight", "every 0 days", "every! mon", "every mon,mon", "every month on 32", "every day until never", "every day for 0"} {
		_, err := Parse(in, untilISO)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("Parse(%q) = %v, want *Error", in, err)
		}
	}
}

// TestEndsAndOverdue: until / for end the series; a late `every` skips
// past dates, `every!` never does.
func TestEndsAndOverdue(t *testing.T) {
	r, err := Parse("every week until 2026-11-20", untilISO)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Upcoming(day("2026-11-04"), 5); len(got) != 3 {
		t.Fatalf("upcoming %v", got)
	}
	r, _ = Parse("every month for 2", untilISO)
	if !r.Ends(3, day("2027-01-01")) || r.Ends(2, day("2027-01-01")) {
		t.Fatal("for 2")
	}
	r, _ = Parse("every week", untilISO)
	// due 11-04, done 11-26: 11-11, 11-18, 11-25 are past → 12-02, 3 skipped
	if d, skipped := r.After(day("2026-11-04"), day("2026-11-26")); d.Format("2006-01-02") != "2026-12-02" || skipped != 3 {
		t.Fatalf("after %s skipped %d", d, skipped)
	}
	r, _ = Parse("every! 3 days", untilISO)
	if d, s := r.After(day("2026-11-01"), day("2026-11-26")); s != 0 || d.Format("2006-01-02") != "2026-11-04" {
		t.Fatalf("every! %s %d", d, s)
	}
	if !strings.Contains(Forms, "every!") {
		t.Fatal("forms")
	}
}

// TestZoneFree: the same instants give the same days in any TZ (TSQ11).
func TestZoneFree(t *testing.T) {
	r, _ := Parse("every month on last", untilISO)
	base := time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC)
	for _, off := range []int{-8, 0, 5, 14} {
		in := base.In(time.FixedZone("z", off*3600))
		if got := r.Next(in).Format("2006-01-02"); got != "2026-12-31" {
			t.Fatalf("offset %d: %s", off, got)
		}
	}
}
