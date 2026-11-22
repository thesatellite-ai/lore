package ask

// prior.go — the date phrase of a question with other words is a soft
// time prior, not a filter (devco D-897, bug hunt H-150).
//
//	"what certification did I complete last month?"
//	  ─▶ words certification, complete ─▶ best rows by words (topRows)
//	  ─▶ + up to priorBonus for a row dated in the window, fading to 0
//	     at fuzz days outside it ─▶ then the window's other rows
//	"what happened yesterday" (no word but the date)
//	  ─▶ the window's rows, oldest first, as before
//
// Why: the phrase names when the thing happened, not when it was written
// down. As a filter on the written date "last month" returned nothing
// when the note was written this month, and "last weekend" returned that
// week's rows oldest first, whatever they said (LongMemEval_S session
// recall_all@5 0.728 -> 0.828, temporal questions 0.764 -> 0.843).

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
)

// priorBonus is the score a row dated inside the question's window gets:
// one more question word (score gives a word 3).
const priorBonus = 3

// monthNames maps a month name or its short form to the month.
var monthNames = map[string]time.Month{
	"january": 1, "jan": 1, "february": 2, "feb": 2, "march": 3, "mar": 3, "april": 4, "apr": 4, "may": 5, "june": 6, "jun": 6,
	"july": 7, "jul": 7, "august": 8, "aug": 8, "september": 9, "sep": 9, "sept": 9, "october": 10, "oct": 10, "november": 11, "nov": 11, "december": 12, "dec": 12,
}

const (
	monthRe = `(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sept|sep|oct|nov|dec)`
	countRe = `(\d{1,3}|a|an|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|a couple of|a couple|couple of|a few|few|several)`
)

var (
	rePThisWeekend = regexp.MustCompile(`\b(?:this|that|next|coming|upcoming) weekend\b`)
	rePNextAny     = regexp.MustCompile(`\b(?:next|coming|upcoming) (?:week|month|year)\b|\btomorrow\b`)
	rePLastWeekend = regexp.MustCompile(`\b(?:last|past|this past|previous|the other) weekend\b|\bover the weekend\b`)
	rePAgo         = regexp.MustCompile(`\b` + countRe + ` (days?|weeks?|months?|years?) ago\b`)
	rePPast        = regexp.MustCompile(`\b(?:past|last|previous) (?:` + countRe + ` )?(days|weeks|months|years|week|month|year)\b`)
	rePBeforeToday = regexp.MustCompile(`\b(?:before|until|up to|prior to) (?:today|now)\b`)
	rePEarlier     = regexp.MustCompile(`\bearlier this (week|month|year)\b`)
	rePLastYear    = regexp.MustCompile(`\blast year\b`)
	rePThisYear    = regexp.MustCompile(`\bthis year\b`)
	rePMonthDay    = regexp.MustCompile(`\b` + monthRe + `\.? (\d{1,2})(?:st|nd|rd|th)?\b`)
	rePDayOfMonth  = regexp.MustCompile(`\b(\d{1,2})(?:st|nd|rd|th)? of ` + monthRe + `\b`)
	rePInMonth     = regexp.MustCompile(`\b(in|during|since|of|early|late|mid|until|through|throughout|by|before|after|and|or|to)[ -]` + monthRe + `\b`)
	rePHoliday     = regexp.MustCompile(`\b(valentine'?s?(?: day)?|christmas eve|christmas|xmas|new year'?s eve|new year'?s(?: day)?|halloween|thanksgiving|independence day|(?:the )?(?:fourth|4th) of july|st\.? patrick'?s day|labor day|memorial day|mother'?s day|father'?s day)\b`)
	rePBeforeAfter = regexp.MustCompile(`\b(before|after|since|until|by)\s*$`)
	// reHow is the phrase inside windowOf's reading ("2026-11-19
	// (yesterday)"): its words are the date, not question words
	reHow = regexp.MustCompile(`\(([^)]*)\)\s*$`)
)

// countOf reads a count of the prior grammar ("a couple of" is 2).
func countOf(s string) int {
	switch strings.TrimSpace(s) {
	case "a", "an", "one":
		return 1
	case "a couple of", "a couple", "couple of":
		return 2
	case "a few", "few":
		return 3
	case "several":
		return 4
	case "eleven":
		return 11
	case "twelve":
		return 12
	}
	return count(s)
}

// dayOfT is t's day (UTC midnight).
func dayOfT(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// spanW is the window of the days from..to (both kept).
func spanW(from, to time.Time) dates.Window {
	return dates.Window{Since: from, End: to.AddDate(0, 0, 1)}
}

// lastOccurrence is the newest date month/day on or before ref (this
// year's, else last year's): a question asks about what happened.
func lastOccurrence(m time.Month, d int, ref time.Time) time.Time {
	t := time.Date(ref.Year(), m, d, 0, 0, 0, 0, time.UTC)
	if t.After(ref) {
		t = t.AddDate(-1, 0, 0)
	}
	return t
}

// holiday is the day of a holiday name in year y.
func holiday(name string, y int) (time.Time, bool) {
	d := func(m time.Month, day int) (time.Time, bool) { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC), true }
	nth := func(m time.Month, wd time.Weekday, n int) (time.Time, bool) {
		t := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		for t.Weekday() != wd {
			t = t.AddDate(0, 0, 1)
		}
		if n < 0 { // the last one of the month
			for t.AddDate(0, 0, 7).Month() == m {
				t = t.AddDate(0, 0, 7)
			}
			return t, true
		}
		return t.AddDate(0, 0, 7*(n-1)), true
	}
	n := strings.ReplaceAll(strings.ReplaceAll(name, "'", ""), ".", "")
	switch {
	case strings.HasPrefix(n, "valentine"):
		return d(2, 14)
	case n == "christmas eve":
		return d(12, 24)
	case n == "christmas" || n == "xmas":
		return d(12, 25)
	case strings.HasPrefix(n, "new years eve"):
		return d(12, 31)
	case strings.HasPrefix(n, "new year"):
		return d(1, 1)
	case n == "halloween":
		return d(10, 31)
	case n == "thanksgiving":
		return nth(11, time.Thursday, 4)
	case n == "independence day" || strings.Contains(n, "of july"):
		return d(7, 4)
	case strings.HasPrefix(n, "st patrick"):
		return d(3, 17)
	case n == "labor day":
		return nth(9, time.Monday, 1)
	case n == "memorial day":
		return nth(5, time.Monday, -1)
	case strings.HasPrefix(n, "mother"):
		return nth(5, time.Sunday, 2)
	case strings.HasPrefix(n, "father"):
		return nth(6, time.Sunday, 3)
	}
	return time.Time{}, false
}

// lastWeekend is the newest Saturday-Sunday that starts before today.
func lastWeekend(today time.Time) dates.Window {
	d := today.AddDate(0, 0, -1)
	for d.Weekday() != time.Saturday {
		d = d.AddDate(0, 0, -1)
	}
	return spanW(d, d.AddDate(0, 0, 1))
}

// thisWeekend is the Saturday-Sunday today is part of (today is one).
func thisWeekend(today time.Time) dates.Window {
	if today.Weekday() == time.Sunday {
		return spanW(today.AddDate(0, 0, -1), today)
	}
	return spanW(today, today.AddDate(0, 0, 1))
}

// unitDays is the length of a unit in days.
func unitDays(u string) int {
	switch strings.TrimSuffix(u, "s") {
	case "week":
		return 7
	case "month":
		return 30
	case "year":
		return 365
	}
	return 1
}

// agoWindow is "n <unit>s ago" against day: about two weeks wide for
// months, two months for years, the day itself for days; fuzz says how
// far around it still counts.
func agoWindow(n int, unit string, day time.Time) (dates.Window, float64) {
	switch strings.TrimSuffix(unit, "s") {
	case "month":
		c := day.AddDate(0, -n, 0)
		return spanW(c.AddDate(0, 0, -7), c.AddDate(0, 0, 7)), 10
	case "year":
		c := day.AddDate(-n, 0, 0)
		return spanW(c.AddDate(0, 0, -30), c.AddDate(0, 0, 30)), 60
	}
	c := day.AddDate(0, 0, -n)
	return spanW(c, c), 1 + float64(n)/5
}

// pmatch is one date phrase read as a window.
type pmatch struct {
	w      dates.Window
	how    string
	fuzz   float64
	phrase string
	// none: the phrase names the future or all of the past ("next week",
	// "before today"): nothing is written there yet, so no window
	none bool
}

// whenLabel says how window w was read: "2026-11-14 to 2026-11-15 (last
// weekend)", "before 2026-03-03 (before march 3rd)".
func whenLabel(w dates.Window, how string) string {
	last := w.End.AddDate(0, 0, -1)
	switch {
	case w.Since.IsZero():
		return "before " + w.End.Format(dates.DayLayout) + " (" + how + ")"
	case w.End.IsZero():
		return "from " + w.Since.Format(dates.DayLayout) + " (" + how + ")"
	case last.Equal(w.Since):
		return w.Since.Format(dates.DayLayout) + " (" + how + ")"
	}
	return w.Since.Format(dates.DayLayout) + " to " + last.Format(dates.DayLayout) + " (" + how + ")"
}

// priorWindowOf reads the date phrases windowOf does not (weekends, N
// months or years ago, the past N weeks, a month, a month and day, a
// holiday, before or after a date) in a question (lower case) against
// now's day. ok false when it reads none (windowOf's answer then stands).
func priorWindowOf(low string, now time.Time) (pmatch, bool) {
	today := dayOfT(now)
	mk := func(w dates.Window, how string, fuzz float64) (pmatch, bool) {
		return pmatch{w: w, how: whenLabel(w, how), fuzz: fuzz, phrase: how}, true
	}
	if m := rePHoliday.FindString(low); m != "" {
		if t, ok := holiday(m, today.Year()); ok {
			if t.After(today) {
				t, _ = holiday(m, today.Year()-1)
			}
			return mk(spanW(t, t), m, 1)
		}
	}
	if m := rePBeforeToday.FindString(low); m != "" {
		return pmatch{none: true, phrase: m}, true
	}
	if m := rePLastWeekend.FindString(low); m != "" {
		return mk(lastWeekend(today), m, 2)
	}
	if m := rePThisWeekend.FindString(low); m != "" {
		if strings.HasPrefix(m, "this") || strings.HasPrefix(m, "that") {
			if wd := today.Weekday(); wd == time.Saturday || wd == time.Sunday {
				return mk(thisWeekend(today), m, 1)
			}
		}
		return pmatch{none: true, phrase: m}, true // the future: nothing written yet
	}
	if m := rePNextAny.FindString(low); m != "" {
		return pmatch{none: true, phrase: m}, true
	}
	if m := rePAgo.FindStringSubmatch(low); m != nil && !strings.HasPrefix(m[2], "week") && !strings.HasPrefix(m[2], "day") {
		// N days and N weeks ago stay windowOf's day and calendar week
		if n := countOf(m[1]); n > 0 {
			w, f := agoWindow(n, m[2], today)
			return mk(w, m[0], f)
		}
	}
	if m := rePPast.FindStringSubmatch(low); m != nil {
		n := 1
		if m[1] != "" {
			n = countOf(m[1])
		}
		singular := !strings.HasSuffix(m[2], "s")
		// last week / last month: windowOf's calendar range
		calendar := m[1] == "" && singular && strings.HasPrefix(m[0], "last ")
		if n > 0 && !calendar && (m[1] != "" || singular) {
			days := n * unitDays(m[2])
			return mk(spanW(today.AddDate(0, 0, -days), today), m[0], math.Max(2, float64(days)/10))
		}
	}
	if m := rePEarlier.FindStringSubmatch(low); m != nil {
		return mk(spanW(today.AddDate(0, 0, -unitDays(m[1])), today), m[0], 2)
	}
	if m := rePLastYear.FindString(low); m != "" {
		y := today.Year() - 1
		return mk(spanW(time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)), m, 30)
	}
	if m := rePThisYear.FindString(low); m != "" {
		return mk(spanW(time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.UTC), today), m, 7)
	}
	if m := rePMonthDay.FindStringSubmatchIndex(low); m != nil {
		mo, d := monthNames[low[m[2]:m[3]]], count(low[m[4]:m[5]])
		if mo != 0 && d >= 1 && d <= 31 {
			t := lastOccurrence(mo, d, today)
			return withSide(low, m[0], spanW(t, t), low[m[0]:m[1]], 2)
		}
	}
	if m := rePDayOfMonth.FindStringSubmatchIndex(low); m != nil {
		d, mo := count(low[m[2]:m[3]]), monthNames[low[m[4]:m[5]]]
		if mo != 0 && d >= 1 && d <= 31 {
			t := lastOccurrence(mo, d, today)
			return withSide(low, m[0], spanW(t, t), low[m[0]:m[1]], 2)
		}
	}
	if ms := rePInMonth.FindAllStringSubmatchIndex(low, -1); ms != nil {
		var w dates.Window
		var names []string
		at := -1
		for _, m := range ms {
			name, prep := low[m[4]:m[5]], low[m[2]:m[3]]
			if !monthPrep(name, prep) {
				continue // "and may I": the verb; "to Jan": a person
			}
			first := time.Date(today.Year(), monthNames[name], 1, 0, 0, 0, 0, time.UTC)
			if first.After(today) {
				first = first.AddDate(-1, 0, 0)
			}
			if w.IsZero() || first.Before(w.Since) {
				w.Since = first
			}
			if e := first.AddDate(0, 1, 0); w.End.IsZero() || e.After(w.End) {
				w.End = e
			}
			if at < 0 {
				at = m[0]
			}
			names = append(names, name)
		}
		if len(names) > 0 {
			return withSide(low, at, w, strings.Join(names, " "), 5)
		}
	}
	return pmatch{}, false
}

// monthPrep reports whether prep before month name name makes it a
// month: a full name after any of rePInMonth's words ("end of march"), a
// short form (jan, dec) or may only after a word of time (in, during,
// since, early, late, mid, until, through, throughout; also before and
// after for a short form). Why: "what did I send to Jan" names a person
// and "and may I" is the verb; read as January they lost the word and
// ranked by a month nobody asked about.
func monthPrep(name, prep string) bool {
	switch prep {
	case "in", "during", "since", "early", "late", "mid", "until", "through", "throughout":
		return true
	case "before", "after":
		return name != "may"
	}
	return len(name) > 3 && name != "may"
}

// withSide turns a date's window into everything before or after it
// when the word before the date says so ("before march 3rd").
func withSide(low string, at int, w dates.Window, phrase string, fuzz float64) (pmatch, bool) {
	if m := rePBeforeAfter.FindStringSubmatch(low[:at]); m != nil {
		switch m[1] {
		case "before", "until", "by":
			w = dates.Window{End: w.Since}
		case "after", "since":
			w = dates.Window{Since: w.End}
		}
		phrase = m[1] + " " + phrase
	}
	return pmatch{w: w, how: whenLabel(w, phrase), fuzz: fuzz, phrase: phrase}, true
}

// readDates sets q's window from the date phrases windowOf does not read
// (priorWindowOf), the fuzz around it a row near it still counts, and
// drops the words of the date phrase from the question's words. A named
// date (a month, weekday, holiday, "weekend") stays a question word too:
// a row saying "in February" or "on Valentine's Day" holds it; only the
// counting words (past, few, weeks, ago) go.
func readDates(q *question, low string) {
	phrase := ""
	if p, ok := priorWindowOf(low, q.now); ok {
		phrase = p.phrase
		if p.none {
			q.window, q.when = dates.Window{}, ""
		} else {
			q.window, q.when, q.fuzz = p.w, p.how, p.fuzz
		}
	} else if !q.window.IsZero() {
		// windowOf's day, week or month
		switch days := q.window.End.Sub(q.window.Since).Hours() / 24; {
		case days <= 1:
			q.fuzz = 2
		case days <= 8:
			q.fuzz = 3
		default:
			q.fuzz = 7
		}
		if m := reHow.FindStringSubmatch(q.when); m != nil {
			phrase = m[1]
		}
	}
	if phrase == "" {
		return
	}
	used := map[string]bool{}
	for _, w := range reWord.FindAllString(phrase, -1) {
		used[strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "'")] = true
	}
	used["valentine"] = used["valentine"] || used["valentines"]
	ws := q.words[:0:0]
	for _, w := range q.words {
		if !used[w] || namedDate(w) {
			ws = append(ws, w)
		}
	}
	q.words = ws
}

// timeBonus is row r's rank bonus for q's date window: priorBonus inside
// it, falling to 0 at fuzz days outside; 0 for a question with no window.
func timeBonus(q question, r *textRow) int {
	if q.window.IsZero() {
		return 0
	}
	day := rowDayT(r)
	if day.IsZero() {
		return 0
	}
	b := priorBonus * (1 - gapDays(q.window, spanW(day, day))/(q.fuzz+1))
	if b <= 0 {
		return 0
	}
	return int(math.Round(b))
}

// thenCurrent keeps a row dated inside the question's window, or near
// enough to rank as dated then (timeBonus, devco D-903), current even
// when a newer row replaced it (markReplaced): what was true then is the
// answer to a question about then. Why: markReplaced chains a series of
// similar notes (meetings, weekly status updates) into one fact replaced
// again and again, so "what meetings did I have two weeks ago" got the
// newest meeting and one of that week's eight. A row superseded by
// another or past its valid until stays marked (staleNote, D-820).
func thenCurrent(q question, all []scored) {
	if q.window.IsZero() {
		return
	}
	for i := range all {
		if all[i].replacedBy == "" {
			continue
		}
		if timeBonus(q, all[i].row) > 0 {
			all[i].history, all[i].replacedBy = staleNote(all[i].row, q.now), ""
		}
	}
}

// rowDayT is the day a row is about: its day metadata, else its date.
func rowDayT(r *textRow) time.Time {
	if r.Day != "" {
		if t, err := time.Parse(dates.DayLayout, r.Day); err == nil {
			return t
		}
	}
	if r.At.IsZero() {
		return time.Time{}
	}
	return dayOfT(r.At)
}

// gapDays is how many days lie between windows a and b (0 when they
// overlap); an open bound reaches forever.
func gapDays(a, b dates.Window) float64 {
	if !a.End.IsZero() && !b.Since.IsZero() && !a.End.After(b.Since) {
		return b.Since.Sub(a.End).Hours()/24 + 1
	}
	if !b.End.IsZero() && !a.Since.IsZero() && !b.End.After(a.Since) {
		return a.Since.Sub(b.End).Hours()/24 + 1
	}
	return 0
}

// maxFillDays is the longest window whose other rows fill a date
// question's answer after its best rows: a day, a weekend, a week.
const maxFillDays = 8

// priorRows is a date question's rows when it has words besides the
// date: the best rows by words plus the time bonus (topRows), then the
// window's other rows oldest first, cut to top. The window's rows fill
// only a short window (maxFillDays) or an answer the words found nothing
// for: "what did I buy last weekend" lists that weekend's rows when no
// row says "buy", but "the budget for orbit this year" is not padded
// with every row written this year.
func priorRows(q question, ms []match, rows []textRow, v *meaningView, top int) ([]Row, int) {
	out, lead := topRows(q, ms, rows, v, top)
	if len(out) > 0 && (q.window.Since.IsZero() || q.window.End.IsZero() || q.window.End.Sub(q.window.Since).Hours()/24 > maxFillDays) {
		return out, lead
	}
	seen := make(map[string]bool, len(out))
	for _, r := range out {
		seen[r.ID] = true
	}
	for _, r := range windowRows(q, ms, rows) {
		if len(out) >= top {
			break
		}
		if !seen[r.ID] {
			out = append(out, r)
		}
	}
	return out, lead
}

// namedDate reports a date word that names a time rather than counting
// one: a month, weekday, holiday or weekend.
func namedDate(w string) bool {
	if _, ok := monthNames[w]; ok && len(w) > 3 {
		return true
	}
	if _, ok := dates.WeekdayOf(strings.TrimSuffix(w, "s")); ok && len(w) > 3 {
		return true
	}
	switch w {
	case "weekend", "valentine", "christmas", "halloween", "thanksgiving", "easter", "independence", "patrick", "labor", "memorial", "mother", "father", "eve":
		return true
	}
	return false
}

// asksBesidesDate reports whether a date question asks for more than the
// date: a topic, or a word that does not name a matched resource. "what
// happened yesterday" and "alice-ng last week" ask for the window's rows;
// "what did I buy last weekend" for the best rows, dated near it first.
func asksBesidesDate(q question, ms []match) bool {
	if len(q.topics) > 0 {
		return true
	}
	for _, w := range q.words {
		if !namesResource(w, ms) {
			return true
		}
	}
	return false
}
