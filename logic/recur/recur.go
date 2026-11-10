// Package recur is TASK 10.2's closed recurrence rule language and its
// next-date math, pure and in UTC calendar days (TSQ11): every machine
// computes the same next date whatever its zone.
//
//	Parse("every month on last until 2027-03-31", until) ─▶ Rule
//	Rule.Next(base)            next day strictly after base
//	Rule.After(base, today)    Next, then past dates skipped (overdue series)
//	Rule.Upcoming(first, n)    the dates printed on create
//
// base is the scheduled due day for `every`, the completion day for
// `every!`. The caller maps *Error to E_RECUR.
package recur

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Forms is the list of accepted forms, printed with every refusal.
const Forms = `every day | every weekday | every week | every N days|weeks|months | every month | every quarter | every year | every mon[,thu…] | every month on N | every month on last | every! N days|weeks|months, each optionally followed by "until <date>" or "for N"`

// Unit is a step unit.
type Unit string

// The units.
const (
	UnitDay   Unit = "day"
	UnitWeek  Unit = "week"
	UnitMonth Unit = "month"
)

// Rule is one parsed rule. Exactly one of the shapes is set: Every (N
// Unit), Weekdays (mon–fri), Days (a weekday set), MonthDay / LastDay.
type Rule struct {
	Raw string
	// FromDone is `every!`: counted from the completion day.
	FromDone bool
	N        int
	Unit     Unit
	Weekdays bool
	Days     []time.Weekday
	MonthDay int
	LastDay  bool
	// Until (zero = none) is the last allowed day; For (0 = none) the
	// number of instances in the series.
	Until time.Time
	For   int
}

// Error is a refused rule.
type Error struct {
	Input, Why string
}

// Error implements error.
func (e *Error) Error() string {
	return strconv.Quote(e.Input) + " is not a recurrence rule lore reads (" + e.Why + "). Use one of: " + Forms
}

var (
	reFor   = regexp.MustCompile(`^(.*)\s+for\s+(\d{1,4})$`)
	reUntil = regexp.MustCompile(`^(.*)\s+until\s+(.+)$`)
	reStep  = regexp.MustCompile(`^(\d{1,3})\s+(days?|weeks?|months?)$`)
	reOn    = regexp.MustCompile(`^month on (\d{1,2}|last)$`)
)

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// Day is midnight UTC of t's UTC calendar day.
func Day(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// Parse reads a rule; until reads the "until" date (TASK 12 phrases, the
// caller's parser) and returns its UTC day.
func Parse(raw string, until func(string) (time.Time, error)) (Rule, error) {
	in := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
	bad := func(why string) (Rule, error) { return Rule{}, &Error{Input: raw, Why: why} }
	r := Rule{Raw: in}
	if m := reFor.FindStringSubmatch(in); m != nil {
		n, _ := strconv.Atoi(m[2])
		if n < 1 {
			return bad("for needs at least 1")
		}
		r.For, in = n, m[1]
	}
	if m := reUntil.FindStringSubmatch(in); m != nil {
		if until == nil {
			return bad("until is not allowed here")
		}
		u, err := until(m[2])
		if err != nil {
			return bad("until " + m[2] + ": " + err.Error())
		}
		r.Until, in = Day(u), m[1]
	}
	switch {
	case strings.HasPrefix(in, "every! "):
		r.FromDone, in = true, strings.TrimPrefix(in, "every! ")
	case strings.HasPrefix(in, "every "):
		in = strings.TrimPrefix(in, "every ")
	default:
		return bad(`a rule starts with "every" or "every!"`)
	}
	switch in {
	case "day":
		r.N, r.Unit = 1, UnitDay
	case "week":
		r.N, r.Unit = 1, UnitWeek
	case "month":
		r.N, r.Unit = 1, UnitMonth
	case "quarter":
		r.N, r.Unit = 3, UnitMonth
	case "year":
		r.N, r.Unit = 12, UnitMonth
	case "weekday":
		r.Weekdays = true
	}
	if r.Unit == "" && !r.Weekdays {
		if m := reStep.FindStringSubmatch(in); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n < 1 {
				return bad("the step must be at least 1")
			}
			r.N, r.Unit = n, Unit(strings.TrimSuffix(m[2], "s"))
		} else if m := reOn.FindStringSubmatch(in); m != nil {
			if m[1] == "last" {
				r.LastDay = true
			} else if d, _ := strconv.Atoi(m[1]); d >= 1 && d <= 31 {
				r.MonthDay = d
			} else {
				return bad("a month day is 1 to 31 or last")
			}
		} else if days, ok := weekdaySet(in); ok {
			r.Days = days
		} else {
			return bad("not in the table")
		}
	}
	if r.FromDone && r.Unit == "" {
		return bad("every! takes a step: every! 3 days, every! 2 weeks")
	}
	return r, nil
}

// weekdaySet reads "mon" or "mon,thu" (Monday-first, no repeats).
func weekdaySet(s string) ([]time.Weekday, bool) {
	seen := map[time.Weekday]bool{}
	var out []time.Weekday
	for _, p := range strings.Split(strings.ReplaceAll(s, " ", ""), ",") {
		d, ok := weekdayNames[p]
		if !ok || seen[d] {
			return nil, false
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, len(out) > 0
}

// Next is the next day strictly after base's UTC day.
func (r Rule) Next(base time.Time) time.Time {
	b := Day(base)
	switch {
	case r.Unit == UnitDay:
		return b.AddDate(0, 0, r.N)
	case r.Unit == UnitWeek:
		return b.AddDate(0, 0, 7*r.N)
	case r.Unit == UnitMonth:
		return addMonths(b, r.N)
	case r.Weekdays:
		d := b.AddDate(0, 0, 1)
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, 1)
		}
		return d
	case len(r.Days) > 0:
		for i := 1; i <= 7; i++ {
			d := b.AddDate(0, 0, i)
			for _, w := range r.Days {
				if d.Weekday() == w {
					return d
				}
			}
		}
	case r.LastDay:
		end := monthEnd(b)
		if end.After(b) {
			return end
		}
		return monthEnd(end.AddDate(0, 0, 1))
	case r.MonthDay > 0:
		for m := 0; m < 3; m++ {
			first := time.Date(b.Year(), b.Month()+time.Month(m), 1, 0, 0, 0, 0, time.UTC)
			d := clampDay(first, r.MonthDay)
			if d.After(b) {
				return d
			}
		}
	}
	return b.AddDate(0, 0, 1)
}

// After is the next instance's due day after an instance due on base
// (or done on base, every!), never before today: past dates are skipped
// and counted (10.4 "overdue series").
func (r Rule) After(base, today time.Time) (time.Time, int) {
	d := r.Next(base)
	t, skipped := Day(today), 0
	for !r.FromDone && d.Before(t) {
		d, skipped = r.Next(d), skipped+1
	}
	return d, skipped
}

// Ends reports whether an instance number index (1-based) due on day
// would be past the end of the series.
func (r Rule) Ends(index int, day time.Time) bool {
	return (r.For > 0 && index > r.For) || (!r.Until.IsZero() && Day(day).After(r.Until))
}

// Upcoming lists up to n due days starting at first, inside the series.
func (r Rule) Upcoming(first time.Time, n int) []time.Time {
	var out []time.Time
	d := Day(first)
	for i := 1; i <= n && !r.Ends(i, d); i++ {
		out = append(out, d)
		d = r.Next(d)
	}
	return out
}

// monthEnd is the last day of t's month.
func monthEnd(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
}

// clampDay is day d of first's month, or its last day when shorter.
func clampDay(first time.Time, d int) time.Time {
	if end := monthEnd(first); d > end.Day() {
		return end
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.UTC)
}

// addMonths adds n months, clamping to the target month's end.
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	return clampDay(first, t.Day())
}

// Canonical is the rule as it is stored: the step part of Raw, then
// "until YYYY-MM-DD" (the phrase already resolved, so "until fri" read
// next month still means the same day) and "for N". Parse(Canonical())
// gives the same rule on every machine.
func (r Rule) Canonical() string {
	in := r.Raw
	if m := reFor.FindStringSubmatch(in); m != nil {
		in = m[1]
	}
	if m := reUntil.FindStringSubmatch(in); m != nil {
		in = m[1]
	}
	if !r.Until.IsZero() {
		in += " until " + r.Until.Format(dayLayout)
	}
	if r.For > 0 {
		in += " for " + strconv.Itoa(r.For)
	}
	return in
}

// dayLayout is the stored calendar-day form.
const dayLayout = "2006-01-02"
