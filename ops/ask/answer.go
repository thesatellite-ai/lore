package ask

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/plural"
)

// taskRow is one open task as the lens holds it, with its debt metadata
// and its --ref pointers at resources.
type taskRow struct {
	ID, Place, Title, Status, Created string
	OwedBy, OwedTo                    string
	// Refs are the resources the task points at (knowledge_refs), with
	// the pointer's relation.
	Refs []taskRef
}

// taskRef is one --ref pointer of a task.
type taskRef struct{ Relation, ResID string }

// textRow is one row with text: a memory, a decision, a rule …
type textRow struct {
	Table, ID, Place, Title, Body string
	At                            time.Time
	// Imported marks a row brought in from a feed (source_kind imported):
	// noise next to what a person wrote, so it ranks after it.
	Imported bool
	// Superseded marks a row another row replaced.
	Superseded bool
	// Day is the row's day metadata (YYYY-MM-DD), "" when none.
	Day string
	// Refs are the resources the row's text references (row_refs).
	Refs []string
}

// data is everything one ask reads.
type data struct {
	resources []resourceRow
	tasks     []taskRow
	rows      []textRow
}

// Defaults of ask.
const (
	// DefaultMaxChars is --max-chars' default: an answer an agent reads in
	// one go, lighter than the files a grep would open.
	DefaultMaxChars = 4000
	// DefaultTop is how many rows are printed in full (--top).
	DefaultTop = 3
	// maxLinked caps the resources listed under one resource.
	maxLinked = 12
	// maxSideTasks caps the open tasks listed under a question that is
	// not about debts.
	maxSideTasks = 5
)

// scored is one text row with its score and the terms it matched.
type scored struct {
	row     *textRow
	score   int
	matched []string
}

// build answers q from d: the matched resources, the open tasks that
// answer it, then the rows (the window's rows for a date question, else
// the best top rows), cut to maxChars of text.
func build(q question, d data, top, maxChars int) Out {
	if top <= 0 {
		top = DefaultTop
	}
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	out := Out{Query: q.text, When: q.when, Resources: []Resource{}, Tasks: []Task{}, Rows: []Row{}, Cited: []string{}}
	ms := matchResources(q, d.resources)
	identity := isIdentity(q.text) && len(ms) > 0
	for _, m := range ms {
		out.Resources = append(out.Resources, resourceOf(m, d.resources, q.owe != oweNone))
	}
	if !identity {
		out.Tasks = tasksFor(q, ms, d.tasks)
	}
	switch {
	case q.owe != oweNone || identity:
		// a debt question is answered by the tasks, "who is X" by the
		// resource: rows would only make the answer heavier
	case !q.window.IsZero():
		out.Rows = windowRows(q, ms, d.rows)
	default:
		out.Rows = topRows(q, ms, d.rows, top)
	}
	out.cap(maxChars)
	for _, r := range out.Resources {
		out.Cited = append(out.Cited, r.ID)
	}
	for _, t := range out.Tasks {
		out.Cited = append(out.Cited, t.ID)
	}
	for _, r := range out.Rows {
		out.Cited = append(out.Cited, r.ID)
	}
	return out
}

// isIdentity reports a "who is X" question: the resource answers it.
func isIdentity(text string) bool {
	low := strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{"who is ", "who's ", "who are ", "what is the email", "what is the role"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// resourceOf is resource m as the answer prints it: its fields (a changed
// field with its former value and the day it changed) and the resources
// whose fields name it (the people of a company, the projects a person
// owns). brief keeps only the name line (a debt question).
func resourceOf(m match, all []resourceRow, brief bool) Resource {
	r := m.res
	out := Resource{ID: r.ID, Kind: r.Kind, Name: r.Name, Place: r.Place, Match: m.how, Fields: []Field{}, Linked: []Linked{}}
	if brief {
		return out
	}
	for _, f := range r.Fields {
		fo := Field{Name: f.Name, Value: f.Value}
		for _, h := range slices.Backward(r.History) {
			if h.Field == f.Name && h.From != "" && h.To == f.Value {
				fo.Was, fo.ChangedAt = h.From, dayOf(h.At)
				break
			}
		}
		out.Fields = append(out.Fields, fo)
	}
	name := strings.ToLower(r.Name)
	for i := range all {
		o := &all[i]
		if o.ID == r.ID {
			continue
		}
		for _, f := range o.Fields {
			if strings.ToLower(strings.TrimSpace(f.Value)) != name {
				continue
			}
			l := Linked{ID: o.ID, Kind: o.Kind, Name: o.Name, Via: f.Name}
			for _, g := range o.Fields {
				if g.Name != f.Name && g.Value != "" && !strings.Contains(g.Value, "@") {
					l.Fields = append(l.Fields, g.Name+" "+g.Value)
				}
			}
			out.Linked = append(out.Linked, l)
			break
		}
		if len(out.Linked) >= maxLinked {
			break
		}
	}
	return out
}

// dayOf is the YYYY-MM-DD of an RFC 3339 time, or s itself.
func dayOf(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// tasksFor lists the open tasks that answer q: for a debt question every
// open task owed in that direction (to or by the resources it names), for
// any other question the open tasks of the resources it names.
func tasksFor(q question, ms []match, ts []taskRow) []Task {
	names, ids := map[string]bool{}, map[string]bool{}
	for _, m := range ms {
		names[strings.ToLower(m.res.Name)], ids[m.res.ID] = true, true
	}
	linked := func(t taskRow, relation string) bool {
		for _, r := range t.Refs {
			if ids[r.ResID] && (relation == "" || r.Relation == relation) {
				return true
			}
		}
		return false
	}
	var out []Task
	for _, t := range ts {
		by, to := strings.ToLower(t.OwedBy), strings.ToLower(t.OwedTo)
		keep := false
		switch q.owe {
		case oweIOwe:
			keep = by == Me && (len(ms) == 0 || names[to] || linked(t, "owed_to"))
		case oweOwesMe:
			keep = to == Me && (len(ms) == 0 || names[by] || linked(t, "owed_by"))
		default:
			keep = len(ms) > 0 && (names[by] || names[to] || linked(t, ""))
		}
		if keep {
			out = append(out, Task{ID: t.ID, Title: t.Title, Status: t.Status, Place: t.Place, OwedBy: t.OwedBy, OwedTo: t.OwedTo, Created: t.Created})
		}
	}
	slices.SortStableFunc(out, func(a, b Task) int { return cmp.Compare(b.Created, a.Created) })
	if q.owe == oweNone && len(out) > maxSideTasks {
		out = out[:maxSideTasks]
	}
	if out == nil {
		out = []Task{}
	}
	return out
}

// subjectOf reports whether row r is about one of the matched resources:
// its text names one, or it references one.
func subjectOf(r *textRow, toks map[string]bool, ms []match) bool {
	for _, m := range ms {
		if toks[strings.ToLower(m.res.Name)] || slices.Contains(r.Refs, m.res.ID) {
			return true
		}
	}
	return false
}

// tokensOf is the word set of a row's text.
func tokensOf(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range reWord.FindAllString(strings.ToLower(s), -1) {
		out[strings.TrimSuffix(w, "'s")] = true
	}
	return out
}

// hasWord reports whether toks holds w or a form of it (appointment for
// appointments, budgets for budget).
func hasWord(toks map[string]bool, w string) bool {
	if toks[w] {
		return true
	}
	if len(w) < 4 {
		return false
	}
	for t := range toks {
		if (strings.HasPrefix(t, w) && len(t)-len(w) <= 3) || (len(t) >= 4 && strings.HasPrefix(w, t) && len(w)-len(t) <= 3) {
			return true
		}
	}
	return false
}

// score rates row r for q: about a matched resource (required when the
// question names one), each topic and each word it holds, a little more
// for a topic in its title or first line; a feed row and a replaced row
// rank lower. ok false when it does not answer at all.
func score(q question, ms []match, r *textRow) (scored, bool) {
	text := r.Title + "\n" + r.Body
	toks := tokensOf(text)
	first := tokensOf(r.Title + "\n" + firstLine(r.Body))
	s := scored{row: r}
	if len(ms) > 0 {
		if !subjectOf(r, toks, ms) {
			return s, false
		}
		s.score += 10
	}
	for _, t := range q.topics {
		for _, w := range t.row {
			if hasWord(toks, w) {
				s.score += 4
				s.matched = append(s.matched, t.name)
				if hasWord(first, w) {
					s.score++
				}
				break
			}
		}
	}
	for _, w := range q.words {
		if namesResource(w, ms) {
			continue
		}
		if hasWord(toks, w) {
			s.score += 3
			s.matched = append(s.matched, w)
		}
	}
	if s.score == 0 {
		return s, false
	}
	if len(ms) == 0 && len(s.matched) == 0 {
		return s, false
	}
	if r.Imported {
		s.score -= 2
	}
	if r.Superseded {
		s.score -= 3
	}
	return s, true
}

// namesResource reports whether word w is (a typo of) a matched name.
func namesResource(w string, ms []match) bool {
	for _, m := range ms {
		n := strings.ToLower(m.res.Name)
		if w == n || osa(w, n) <= typoBudget(w) {
			return true
		}
	}
	return false
}

// firstLine is the first line of s.
func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// topRows picks the best top rows for q, newest first among equals; the
// first pick takes the best row of each folder among the best-scored rows,
// so one busy folder (a daily journal) does not hide the others (the
// meeting note about the same person).
func topRows(q question, ms []match, rows []textRow, top int) []Row {
	var all []scored
	for i := range rows {
		if s, ok := score(q, ms, &rows[i]); ok {
			all = append(all, s)
		}
	}
	slices.SortStableFunc(all, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := b.row.At.Compare(a.row.At); c != 0 {
			return c
		}
		return strings.Compare(b.row.ID, a.row.ID)
	})
	if len(all) == 0 {
		return []Row{}
	}
	if len(all[0].matched) > 0 {
		// some rows hold what the question asks (a topic, a word): rows
		// that only name the resource would pad the answer
		all = slices.DeleteFunc(all, func(s scored) bool { return len(s.matched) == 0 })
	}
	var pick []scored
	taken := map[string]bool{}
	places := map[string]bool{}
	for _, s := range all {
		if s.score < all[0].score || len(pick) >= top {
			break
		}
		if !places[s.row.Place] {
			places[s.row.Place], taken[s.row.ID] = true, true
			pick = append(pick, s)
		}
	}
	for _, s := range all {
		if len(pick) >= top {
			break
		}
		if !taken[s.row.ID] {
			taken[s.row.ID] = true
			pick = append(pick, s)
		}
	}
	slices.SortStableFunc(pick, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return b.row.At.Compare(a.row.At)
	})
	out := make([]Row, 0, len(pick))
	for _, s := range pick {
		out = append(out, rowOf(s.row, s.matched))
	}
	// a fact that changed: the newest row on the same thing comes first
	// and says what it replaces
	if len(pick) > 1 {
		for _, s := range pick[1:] {
			if slices.Equal(s.matched, pick[0].matched) && len(s.matched) > 0 && s.row.At.Before(pick[0].row.At) && s.score == pick[0].score {
				out[0].Note = fmt.Sprintf("newest: it replaces %s from %s", s.row.ID, s.row.At.UTC().Format(dates.DayLayout))
				break
			}
		}
	}
	return out
}

// windowRows lists the rows of q's date window, oldest first: the rows
// with a day (a journal) when any has one, else every written row dated
// in it, else the imported ones; only the rows about the matched
// resources when the question names any.
func windowRows(q question, ms []match, rows []textRow) []Row {
	var withDay, written, imported []*textRow
	for i := range rows {
		r := &rows[i]
		if len(ms) > 0 && !subjectOf(r, tokensOf(r.Title+"\n"+r.Body), ms) {
			continue
		}
		if r.Day != "" {
			if t, err := time.Parse(dates.DayLayout, r.Day); err == nil && q.window.Keeps(t) {
				withDay = append(withDay, r)
			}
			continue
		}
		if !q.window.Keeps(r.At) {
			continue
		}
		if r.Imported {
			imported = append(imported, r)
		} else {
			written = append(written, r)
		}
	}
	pick := withDay
	if len(pick) == 0 {
		pick = written
	}
	if len(pick) == 0 {
		pick = imported
	}
	slices.SortStableFunc(pick, func(a, b *textRow) int {
		return cmp.Or(strings.Compare(rowDay(a), rowDay(b)), a.At.Compare(b.At), strings.Compare(a.ID, b.ID))
	})
	out := make([]Row, 0, len(pick))
	for _, r := range pick {
		out = append(out, rowOf(r, nil))
	}
	return out
}

// rowDay is the day a row is about: its day metadata, else its date.
func rowDay(r *textRow) string {
	if r.Day != "" {
		return r.Day
	}
	return r.At.UTC().Format(dates.DayLayout)
}

// rowOf is row r as the answer prints it.
func rowOf(r *textRow, matched []string) Row {
	out := Row{ID: r.ID, Table: r.Table, Place: r.Place, Title: r.Title, Body: r.Body, Matched: matched}
	if r.Day != "" {
		out.At = r.Day
	} else if !r.At.IsZero() {
		out.At = r.At.UTC().Format(dates.DayLayout)
	}
	if out.Matched == nil {
		out.Matched = []string{}
	}
	return out
}

// cap keeps the answer within maxChars of text: resources, then tasks,
// then rows, each kept whole or left out; Left counts what was left out.
func (o *Out) cap(maxChars int) {
	used := len(o.header())
	keep := func(n int) bool {
		if used+n > maxChars {
			return false
		}
		used += n
		return true
	}
	var rs []Resource
	for i, r := range o.Resources {
		if !keep(len(r.text())) {
			o.Left += len(o.Resources) - i
			break
		}
		rs = append(rs, r)
	}
	var ts []Task
	if len(o.Tasks) > 0 {
		used += len(o.tasksHeader())
	}
	for i, t := range o.Tasks {
		if !keep(len(t.text())) {
			o.Left += len(o.Tasks) - i
			break
		}
		ts = append(ts, t)
	}
	var rows []Row
	if len(o.Rows) > 0 {
		used += len(o.rowsHeader())
	}
	for i, r := range o.Rows {
		if !keep(len(r.text(i))) {
			o.Left += len(o.Rows) - i
			break
		}
		rows = append(rows, r)
	}
	o.Resources, o.Tasks, o.Rows = orEmpty(rs), orEmpty(ts), orEmpty(rows)
	o.maxChars = maxChars
}

// orEmpty is s, or an empty (not nil) slice.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Text is the answer as an agent reads it: the question, the resources,
// the open tasks, the rows in full, then the ids it cites.
func (o Out) Text() string {
	var b strings.Builder
	b.WriteString(o.header())
	for _, r := range o.Resources {
		b.WriteString(r.text())
	}
	if len(o.Tasks) > 0 {
		b.WriteString(o.tasksHeader())
		for _, t := range o.Tasks {
			b.WriteString(t.text())
		}
	}
	if len(o.Rows) > 0 {
		b.WriteString(o.rowsHeader())
		for i, r := range o.Rows {
			b.WriteString(r.text(i))
		}
	}
	if len(o.Resources)+len(o.Tasks)+len(o.Rows) == 0 {
		b.WriteString("nothing in the store answers it; try other words, or lore search --query \"<words>\"\n")
	}
	if o.Left > 0 {
		fmt.Fprintf(&b, "\n%s left out to stay under %d characters; --max-chars %d prints more\n", plural.Count(o.Left, "more item"), o.maxChars, o.maxChars*2)
	}
	if len(o.Cited) > 0 {
		b.WriteString("\ncited: " + strings.Join(o.Cited, " ") + "\n")
	}
	return b.String()
}

// header is the first line(s): the question and the date window.
func (o Out) header() string {
	h := "ask: " + o.Query + "\n"
	if o.When != "" {
		h += "dates: " + o.When + "\n"
	}
	return h
}

// tasksHeader heads the open tasks.
func (o Out) tasksHeader() string { return "\nopen tasks (" + fmt.Sprint(len(o.Tasks)) + "):\n" }

// rowsHeader heads the rows.
func (o Out) rowsHeader() string {
	if o.When != "" {
		return "\nrows dated " + o.When + ", oldest first:\n"
	}
	return "\nbest rows, in full:\n"
}

// text is one resource: its line, its fields and what links to it.
func (r Resource) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s %s  %s  (%s)\n", r.Kind, r.Name, r.ID, r.Place)
	for _, f := range r.Fields {
		if f.Value == "" {
			continue
		}
		fmt.Fprintf(&b, "  %s: %s", f.Name, f.Value)
		if f.Was != "" {
			fmt.Fprintf(&b, " (changed %s, was %s)", f.ChangedAt, f.Was)
		}
		b.WriteString("\n")
	}
	if len(r.Linked) > 0 {
		b.WriteString("  linked:\n")
		for _, l := range r.Linked {
			fmt.Fprintf(&b, "    %s %s (%s)", l.Kind, l.Name, l.Via)
			if len(l.Fields) > 0 {
				b.WriteString(": " + strings.Join(l.Fields, ", "))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// text is one task line.
func (t Task) text() string {
	s := fmt.Sprintf("  - [%s] %s  %s  (%s)", t.Status, t.Title, t.ID, t.Place)
	if t.OwedBy != "" || t.OwedTo != "" {
		s += fmt.Sprintf("  owed by %s to %s", cmp.Or(t.OwedBy, "?"), cmp.Or(t.OwedTo, "?"))
	}
	return s + "\n"
}

// text is one row in full.
func (r Row) text(i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%d. %s  %s %s  (%s)", i+1, r.At, r.Table, r.ID, r.Place)
	if r.Note != "" {
		b.WriteString("  " + r.Note)
	}
	b.WriteString("\n")
	if r.Title != "" && r.Title != r.Body {
		b.WriteString("   " + r.Title + "\n")
	}
	for _, line := range strings.Split(strings.TrimRight(r.Body, "\n"), "\n") {
		b.WriteString(strings.TrimRight("   "+line, " ") + "\n")
	}
	return b.String()
}
