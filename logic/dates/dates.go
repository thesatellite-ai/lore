// Package dates is the one fixed table of date phrases every date flag
// reads (TASK 12, T11): pure, clock and zone injected, everything outside
// the table refused (the caller maps Error to E_DATE).
//
//	Parse(raw, Clock{Now, Loc, WeekEnd})
//	  ├─ split an optional trailing HH:MM          → datetime in Loc, UTC
//	  │    (LocalTime: a spring-gap time moves forward by the gap,
//	  │     a repeated fall-back time takes the earlier offset)
//	  ├─ YYYY-MM-DD | RFC3339                      → as given
//	  ├─ today|tomorrow|yesterday                  → Now's day in Loc
//	  ├─ <wd> | this <wd> | next <wd>              → Monday-based weeks
//	  ├─ in N d|w|m|y (or days/weeks/months/years) → month ends clamp
//	  ├─ eow|end of week, eom|end of month
//	  └─ <mon> <d> | <d> <mon>                     → past = next year (noted)
//
// A calendar day is returned as midnight UTC of that day (the UTC calendar
// day, TSQ11); a datetime as its UTC instant.
package dates

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DayLayout is the stored and printed calendar-day form.
const DayLayout = "2006-01-02"

// Forms is the list of accepted forms, printed with every refusal.
const Forms = `YYYY-MM-DD, today, tomorrow, yesterday, mon…sun, this fri, next fri, in 3d / 2w / 1m / 1y (or "in 3 days"), eow / end of week, eom / end of month, nov 20 / 20 nov, any of these + HH:MM, or an RFC3339 datetime`

// Clock is the injected "now": the instant, the zone it is read in, and
// the policy week end (tasks.week_end, default Friday).
type Clock struct {
	Now     time.Time
	Loc     *time.Location
	WeekEnd time.Weekday
}

// Result is one parsed phrase.
type Result struct {
	// At is midnight UTC of the day (Day) or the UTC instant (!Day).
	At time.Time
	// Day is true for a calendar day, false for a datetime.
	Day bool
	// Note is set when the phrase was read in a way worth saying
	// ("nov 2 is past: 2027-11-02").
	Note string
}

// Text is the stored form: YYYY-MM-DD or RFC3339 UTC.
func (r Result) Text() string {
	if r.Day {
		return r.At.Format(DayLayout)
	}
	return r.At.UTC().Format(time.RFC3339)
}

// Error is a refused phrase; Why says what is wrong with it.
type Error struct {
	Input string
	Why   string
}

// Error implements error.
func (e *Error) Error() string {
	return strconv.Quote(e.Input) + " is not a date lore reads (" + e.Why + "). Use one of: " + Forms
}

var (
	reClock = regexp.MustCompile(`^(.*?)\s+([01]?\d|2[0-3]):([0-5]\d)$`)
	reIn    = regexp.MustCompile(`^in\s*(\d{1,4})\s*(d|w|m|y|days?|weeks?|months?|years?)$`)
	reSlash = regexp.MustCompile(`^\d{1,4}[/.]\d{1,2}([/.]\d{1,4})?$`)
	reDay   = regexp.MustCompile(`^\d{1,2}$`)
	reISO   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday, "mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday, "wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday, "sat": time.Saturday, "saturday": time.Saturday,
}

var months = map[string]time.Month{
	"jan": time.January, "january": time.January, "feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March, "apr": time.April, "april": time.April, "may": time.May,
	"jun": time.June, "june": time.June, "jul": time.July, "july": time.July, "aug": time.August, "august": time.August,
	"sep": time.September, "sept": time.September, "september": time.September, "oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November, "dec": time.December, "december": time.December,
}

// WeekdayOf reads a weekday name (mon, monday, …).
func WeekdayOf(s string) (time.Weekday, bool) {
	d, ok := weekdays[strings.ToLower(strings.TrimSpace(s))]
	return d, ok
}

// Parse reads one date phrase against c.
func Parse(raw string, c Clock) (Result, error) {
	in := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
	bad := func(why string) (Result, error) { return Result{}, &Error{Input: raw, Why: why} }
	if in == "" {
		return bad("empty")
	}
	loc := c.Loc
	if loc == nil {
		loc = time.UTC
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
		return Result{At: t.UTC()}, nil
	}
	hh, mm, hasClock := -1, -1, false
	if m := reClock.FindStringSubmatch(in); m != nil {
		hh, _ = strconv.Atoi(m[2])
		mm, _ = strconv.Atoi(m[3])
		in, hasClock = m[1], true
	}
	y, mo, d, note, ok := day(in, c.Now.In(loc), c.WeekEnd)
	if !ok && reISO.MatchString(in) {
		return bad("no such day")
	}
	if !ok {
		if reSlash.MatchString(in) {
			return bad("day/month order is ambiguous")
		}
		if note != "" {
			return bad(note)
		}
		return bad("not in the table")
	}
	if hasClock {
		at, gap := LocalTime(y, mo, d, hh, mm, loc)
		if gap > 0 && note == "" {
			note = fmt.Sprintf("%02d:%02d does not exist on %04d-%02d-%02d in %s (clocks skip ahead): %s",
				hh, mm, y, int(mo), d, loc, at.In(loc).Format("15:04"))
		}
		return Result{At: at, Note: note}, nil
	}
	return Result{At: time.Date(y, mo, d, 0, 0, 0, 0, time.UTC), Day: true, Note: note}, nil
}

// LocalTime is the UTC instant of the wall time y-mo-d hh:mm in loc, with
// the RFC 5545 / java.time rule for the two hours a clock change makes
// odd (PC-TASK-3); time.Date leaves both unspecified:
//   - a wall time inside a spring-forward gap does not exist: it moves
//     forward by the gap length (02:30 on a 1-hour change day = 03:30
//     local; on Lord Howe's 30-minute change 02:15 = 02:45), i.e. it is
//     read with the offset in effect before the gap; gap is that length;
//   - a wall time a fall-back repeats takes the earlier offset (the first
//     of the two instants).
//
// Every offset loc uses within a day of the wall time is tried; the ones
// that give back the same wall time are the real readings.
func LocalTime(y int, mo time.Month, d, hh, mm int, loc *time.Location) (time.Time, time.Duration) {
	wall := time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
	offsetAt := func(t time.Time) time.Duration {
		_, off := t.In(loc).Zone()
		return time.Duration(off) * time.Second
	}
	var offs []time.Duration
	for _, step := range []time.Duration{-24 * time.Hour, 0, 24 * time.Hour} {
		if off := offsetAt(wall.Add(step)); !slices.Contains(offs, off) {
			offs = append(offs, off)
		}
	}
	var best, earliest time.Time
	for _, off := range offs {
		t := wall.Add(-off)
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
		if offsetAt(t) == off && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	if !best.IsZero() {
		return best, 0
	}
	// In the gap: no offset reads back. The offset before the change is
	// the one in effect just before the earliest candidate instant.
	before := offsetAt(earliest.Add(-time.Nanosecond))
	at := wall.Add(-before)
	return at, offsetAt(at) - before
}

// day resolves the date part; on refusal note may carry the reason.
func day(in string, now time.Time, weekEnd time.Weekday) (int, time.Month, int, string, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	ret := func(t time.Time) (int, time.Month, int, string, bool) { return t.Year(), t.Month(), t.Day(), "", true }
	if t, err := time.Parse(DayLayout, in); err == nil {
		return ret(t)
	}
	switch in {
	case "today":
		return ret(today)
	case "tomorrow":
		return ret(today.AddDate(0, 0, 1))
	case "yesterday":
		return ret(today.AddDate(0, 0, -1))
	case "eow", "end of week":
		return ret(today.AddDate(0, 0, (int(weekEnd)-int(today.Weekday())+7)%7))
	case "eom", "end of month":
		return ret(time.Date(today.Year(), today.Month()+1, 0, 0, 0, 0, 0, time.UTC))
	}
	if wd, ok := weekdays[in]; ok {
		n := (int(wd) - int(today.Weekday()) + 7) % 7
		if n == 0 {
			n = 7
		}
		return ret(today.AddDate(0, 0, n))
	}
	if w, rest, ok := strings.Cut(in, " "); ok && (w == "this" || w == "next") {
		wd, ok := weekdays[rest]
		if !ok {
			return 0, 0, 0, "", false
		}
		monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		t := monday.AddDate(0, 0, (int(wd)+6)%7)
		if w == "next" {
			return ret(t.AddDate(0, 0, 7))
		}
		if t.Before(today) {
			return 0, 0, 0, "this " + rest + " is already past; use next " + rest, false
		}
		return ret(t)
	}
	if m := reIn.FindStringSubmatch(in); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2][0] {
		case 'd':
			return ret(today.AddDate(0, 0, n))
		case 'w':
			return ret(today.AddDate(0, 0, 7*n))
		case 'm':
			return ret(AddMonths(today, n))
		default:
			return ret(AddMonths(today, 12*n))
		}
	}
	a, b, ok := strings.Cut(in, " ")
	if !ok {
		return 0, 0, 0, "", false
	}
	mon, okm := months[a]
	ds := b
	if !okm {
		mon, okm = months[b]
		ds = a
	}
	if !okm || !reDay.MatchString(ds) {
		return 0, 0, 0, "", false
	}
	dd, _ := strconv.Atoi(ds)
	t := time.Date(today.Year(), mon, dd, 0, 0, 0, 0, time.UTC)
	if t.Month() != mon || dd == 0 {
		return 0, 0, 0, "no such day", false
	}
	if t.Before(today) {
		t2 := time.Date(today.Year()+1, mon, dd, 0, 0, 0, 0, time.UTC)
		if t2.Month() != mon {
			return 0, 0, 0, "no such day next year", false
		}
		return t2.Year(), t2.Month(), t2.Day(), in + " is past this year: " + t2.Format(DayLayout), true
	}
	return ret(t)
}

// AddMonths adds n months, clamping to the last day of the target month
// (jan 31 + 1m = feb 28/29, never mar 3).
func AddMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	d := t.Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}
