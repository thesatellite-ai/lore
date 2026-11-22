package ask

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/thesatellite-ai/lore/logic/dates"
)

// question is what ask reads out of the question text before it looks at
// any row: the words worth matching, the topics they name, whether it asks
// about debts, and the date window it names.
type question struct {
	text string
	// words are the content words (lower case, question words and filler
	// left out, a possessive 's cut: "falcon's" is falcon), in order.
	words []string
	// tokens are every word of the text (lower case, possessive cut), the
	// ones a resource name, nickname or role can be.
	tokens []string
	// topics are the topic groups the words name (decision, status,
	// budget …): a row holding any word of a group matches it, so "what
	// was the reasoning behind X's pick" finds "we chose … because …".
	topics []topic
	owe    oweDir
	// window is the date range the question names ("yesterday", "last
	// week", "2026-11-02", "the week of 2026-11-02"); zero when none.
	window dates.Window
	// when says how the window was read ("2026-11-19 (yesterday)").
	when string
	// file is the path the question names (file.go); its words are not
	// question words: "db/migrations/0042_invoice.sql" says where, not what.
	file string
	// rest is the question without the path (what the change is for).
	rest string
	// now is the moment the question is asked: a row whose valid_until
	// is not after it is history, not the answer (devco D-820).
	now time.Time
	// about is an everything-about-X question: the words say so
	// ("everything about hollow"), or the question is only the name of a
	// resource ("hollow"). Set by build once the resources are matched.
	about bool
	// fuzz is how many days around window a row still counts as dated
	// near it (prior.go: timeBonus); 0 without a window.
	fuzz float64
}

// oweDir is the direction of a debt question.
type oweDir int

const (
	oweNone   oweDir = iota
	oweIOwe          // "what do I owe X": open tasks owed by me to X
	oweOwesMe        // "what does X owe me", "who owes me": owed to me
)

// Me is the owed_by / owed_to value naming the store's owner (the value
// `lore task add --meta owed_by=me` writes).
const Me = "me"

// topic is one thing a question asks about in words of its own: ask are
// the question words that name it, row the words a row about it holds.
// The two lists differ: "how far along" asks for a status, but a row
// holding "along" ("ran along the river") is no status.
type topic struct {
	name string
	ask  []string
	row  []string
}

// topicTable is the fixed table. Why a table: a question asks in its own
// words ("how far along", "the reasoning behind the pick") while the rows
// say "status update" and "we chose … because"; plain word search finds
// none of them (recall gate, paraphrase questions).
var topicTable = []topic{
	{"decision",
		[]string{"decision", "decisions", "decide", "decided", "chose", "choose", "chosen", "choice", "pick", "picked", "reason", "reasons", "reasoning", "why", "because", "rationale"},
		[]string{"decision", "decided", "chose", "chosen", "choice", "picked", "because", "reason", "rationale"}},
	{"status",
		[]string{"status", "progress", "along", "state", "stage", "update", "updates"},
		[]string{"status", "progress", "stage", "update"}},
	{"budget",
		[]string{"budget", "budgets", "money", "spend", "spending", "cost", "costs", "funds", "funding"},
		[]string{"budget", "cost", "funds", "funding", "spend"}},
	{"meeting",
		[]string{"meeting", "meetings", "met", "talked"},
		[]string{"meeting", "met", "talked"}},
}

// topicOf is the topic a question word names; ok false for none.
func topicOf(w string) (topic, bool) {
	for _, t := range topicTable {
		for _, x := range t.ask {
			if x == w {
				return t, true
			}
		}
	}
	return topic{}, false
}

// skipWords are the words a question is made of that say nothing about
// what it asks for.
var skipWords = map[string]bool{
	"a": true, "about": true, "after": true, "all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "at": true,
	"be": true, "been": true, "before": true, "behind": true, "by": true, "can": true, "could": true, "day": true, "days": true, "did": true,
	"do": true, "does": true, "else": true, "everything": true, "for": true, "from": true, "get": true, "had": true, "has": true,
	"have": true, "how": true, "i": true, "in": true, "into": true, "is": true, "it": true, "its": true, "know": true, "me": true,
	"much": true, "my": true, "now": true, "of": true, "on": true, "or": true, "our": true, "over": true, "show": true, "so": true,
	"tell": true, "than": true, "that": true, "the": true, "their": true, "them": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "to": true, "us": true, "was": true, "we": true, "were": true, "what": true,
	"whats": true, "when": true, "where": true, "which": true, "who": true, "whom": true, "whose": true, "will": true, "with": true,
	"would": true, "you": true, "your": true, "happened": true, "happen": true, "going": true, "far": true, "currently": true,
	"current": true, "latest": true, "newest": true, "today": true, "yesterday": true, "week": true, "last": true, "ago": true,
	"owe": true, "owes": true, "owed": true, "handles": true, "handle": true, "work": true, "works": true, "working": true,
}

// reWord splits a question into words: letters, digits and the inner
// hyphens and dots of names like alice-ng or 2026-11-02.
var reWord = regexp.MustCompile(`[\p{L}\p{N}]+(?:[-.'’][\p{L}\p{N}]+)*`)

// parseQuestion reads q against now (the day relative dates count from).
func parseQuestion(q string, now time.Time) question {
	out := question{text: strings.TrimSpace(q), now: now}
	out.file = fileOf(out.text)
	low := strings.ToLower(out.text)
	if out.file != "" {
		low = strings.Replace(low, strings.ToLower(out.file), " ", 1)
		out.rest = strings.TrimSpace(strings.Replace(out.text, out.file, " ", 1))
	}
	seen := map[string]bool{}
	for _, w := range reWord.FindAllString(low, -1) {
		w = strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "’s")
		w = strings.Trim(w, ".")
		if w == "" {
			continue
		}
		out.tokens = append(out.tokens, w)
		if skipWords[w] || seen[w] || isNumberWord(w) {
			continue
		}
		seen[w] = true
		if t, ok := topicOf(w); ok {
			if !hasTopic(out.topics, t.name) {
				out.topics = append(out.topics, t)
			}
			continue
		}
		if len([]rune(w)) < 3 && !hasDigit(w) {
			continue
		}
		out.words = append(out.words, w)
	}
	out.owe = oweOf(low)
	out.window, out.when = windowOf(low, now)
	readDates(&out, low) // prior.go: weekends, months, holidays; the fuzz
	if !out.window.IsZero() {
		// the date words were the date: "what happened on 2026-11-02"
		// asks for that day's rows, not for rows holding "2026-11-02"
		out.words = dropDates(out.words)
	}
	return out
}

// hasTopic reports whether ts holds the topic named name.
func hasTopic(ts []topic, name string) bool {
	for _, t := range ts {
		if t.name == name {
			return true
		}
	}
	return false
}

// hasDigit reports whether s holds a digit.
func hasDigit(s string) bool { return strings.IndexFunc(s, unicode.IsDigit) >= 0 }

// dropDates leaves out the words that are dates or weekday names.
func dropDates(ws []string) []string {
	out := ws[:0:0]
	for _, w := range ws {
		if reISODay.MatchString(w) {
			continue
		}
		if _, ok := dates.WeekdayOf(strings.TrimSuffix(w, "s")); ok {
			continue
		}
		out = append(out, w)
	}
	return out
}

var (
	reOweMe = regexp.MustCompile(`\bowes? me\b|\bowed to me\b|\bdo(?:es)? \S+ owe me\b`)
	reIOwe  = regexp.MustCompile(`\b(?:i|we) owe\b|\bowed by me\b|\bdo i owe\b`)
)

// oweOf reads the direction of a debt question.
func oweOf(low string) oweDir {
	switch {
	case reOweMe.MatchString(low):
		return oweOwesMe
	case reIOwe.MatchString(low):
		return oweIOwe
	}
	return oweNone
}

var numberWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "a": 1}

// isNumberWord reports a spelled-out count (two Fridays ago).
func isNumberWord(w string) bool {
	_, ok := numberWords[w]
	return ok && w != "a"
}

// weekdayRe is a weekday name, whole ("last month" names no Monday).
const weekdayRe = `(monday|mon|tuesday|tues|tue|wednesday|wed|thursday|thurs|thur|thu|friday|fri|saturday|sat|sunday|sun)`

var (
	reISODay      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reWeekOf      = regexp.MustCompile(`\bweek of (\d{4}-\d{2}-\d{2})\b`)
	reDayISO      = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	reDaysAgo     = regexp.MustCompile(`\b(\d{1,3}|one|two|three|four|five|six|seven|eight|nine|ten|a) days? ago\b`)
	reWeeksAgo    = regexp.MustCompile(`\b(\d{1,3}|one|two|three|four|five|six|seven|eight|nine|ten|a) weeks? ago\b`)
	reWeekdaysAgo = regexp.MustCompile(`\b(\d{1,3}|one|two|three|four|five|six|seven|eight|nine|ten|a) ` + weekdayRe + `s? ago\b`)
	reLastWeekday = regexp.MustCompile(`\blast ` + weekdayRe + `\b`)
	reToday       = regexp.MustCompile(`\btoday\b`)
)

// count reads a count: digits or a spelled-out number.
func count(s string) int {
	if n, ok := numberWords[s]; ok {
		return n
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// windowOf reads the date range a question names, against now's day
// (UTC): a day, "the week of <day>" (Monday to Sunday), yesterday, the
// day before yesterday, N days ago, last week / this week, N weeks ago,
// last month / this month, last <weekday> and N <weekday>s ago (the N-th
// one before today). Zero when it names none.
func windowOf(low string, now time.Time) (dates.Window, string) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	day := func(d time.Time, how string) (dates.Window, string) {
		w := dates.Window{Since: d, End: d.AddDate(0, 0, 1)}
		if how == "" {
			return w, d.Format(dates.DayLayout)
		}
		return w, d.Format(dates.DayLayout) + " (" + how + ")"
	}
	span := func(from, to time.Time, how string) (dates.Window, string) {
		return dates.Window{Since: from, End: to.AddDate(0, 0, 1)}, from.Format(dates.DayLayout) + " to " + to.Format(dates.DayLayout) + " (" + how + ")"
	}
	monday := func(t time.Time) time.Time { return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)) }
	nthWeekdayBack := func(wd time.Weekday, n int) time.Time {
		d := today
		for n > 0 {
			d = d.AddDate(0, 0, -1)
			if d.Weekday() == wd {
				n--
			}
		}
		return d
	}
	if m := reWeekOf.FindStringSubmatch(low); m != nil {
		if t, err := time.Parse(dates.DayLayout, m[1]); err == nil {
			mon := monday(t)
			return span(mon, mon.AddDate(0, 0, 6), "the week of "+m[1])
		}
	}
	if m := reDayISO.FindStringSubmatch(low); m != nil {
		if t, err := time.Parse(dates.DayLayout, m[1]); err == nil {
			return day(t, "")
		}
	}
	switch {
	case strings.Contains(low, "day before yesterday"):
		return day(today.AddDate(0, 0, -2), "the day before yesterday")
	case strings.Contains(low, "yesterday"):
		return day(today.AddDate(0, 0, -1), "yesterday")
	}
	if m := reWeekdaysAgo.FindStringSubmatch(low); m != nil {
		if wd, ok := dates.WeekdayOf(m[2]); ok && count(m[1]) > 0 {
			return day(nthWeekdayBack(wd, count(m[1])), strings.TrimSpace(m[0]))
		}
	}
	if m := reLastWeekday.FindStringSubmatch(low); m != nil {
		if wd, ok := dates.WeekdayOf(m[1]); ok {
			return day(nthWeekdayBack(wd, 1), m[0])
		}
	}
	if m := reDaysAgo.FindStringSubmatch(low); m != nil && count(m[1]) > 0 {
		return day(today.AddDate(0, 0, -count(m[1])), m[0])
	}
	if m := reWeeksAgo.FindStringSubmatch(low); m != nil && count(m[1]) > 0 {
		mon := monday(today).AddDate(0, 0, -7*count(m[1]))
		return span(mon, mon.AddDate(0, 0, 6), m[0])
	}
	switch {
	case strings.Contains(low, "last week"):
		mon := monday(today).AddDate(0, 0, -7)
		return span(mon, mon.AddDate(0, 0, 6), "last week")
	case strings.Contains(low, "this week"):
		return span(monday(today), today, "this week")
	case strings.Contains(low, "last month"):
		first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
		return span(first, first.AddDate(0, 1, -1), "last month")
	case strings.Contains(low, "this month"):
		return span(time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC), today, "this month")
	case reToday.MatchString(low):
		return day(today, "today")
	}
	return dates.Window{}, ""
}
