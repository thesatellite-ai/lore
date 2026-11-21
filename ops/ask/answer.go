package ask

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/schema"
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
	// SupersededBy is the id of the row that replaced this one
	// (superseded_by_id), "" when none.
	SupersededBy string
	// ValidUntil is a memory's valid_until: from then on it is history
	// (lore memory invalidate sets it); zero when unset.
	ValidUntil time.Time
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
	// history says why the row is no longer the current answer, "" when
	// it is current (staleNote, replacedNote).
	history string
	// replacedBy is the id of the newer row that replaced this one on the
	// same thing (markReplaced), "" when none.
	replacedBy string
}

// staleNote says why row r is no longer the current answer as lore
// stores it: superseded by another row, or past its valid_until at now;
// "" when it is current. Why (devco D-820, owner): a fact that changed
// must never be handed to an agent as the answer, only as history.
func staleNote(r *textRow, now time.Time) string {
	return schema.HistoryNote(r.SupersededBy, r.ValidUntil, now)
}

// markReplaced marks the rows of all that a newer current row replaced:
// the same folder and the same topics and words of the question (so the
// same thing: a budget, a gym, a status) but older. Each points at the
// newest such row, which says what it replaces. Rows matching nothing but
// a name are left alone: two rows that only name alice-ng say different
// things.
func markReplaced(all []scored) {
	for i := range all {
		s := &all[i]
		if s.history != "" || len(s.matched) == 0 {
			continue
		}
		var newest *scored
		for j := range all {
			n := &all[j]
			if j == i || n.history != "" || n.row.Place != s.row.Place || !slices.Equal(n.matched, s.matched) || !n.row.At.After(s.row.At) {
				continue
			}
			if newest == nil || n.row.At.After(newest.row.At) {
				newest = n
			}
		}
		if newest == nil {
			continue
		}
		s.history = fmt.Sprintf("history: replaced by %s from %s", newest.row.ID, newest.row.At.UTC().Format(dates.DayLayout))
		s.replacedBy = newest.row.ID
	}
}

// build answers q from d: the matched resources, the open tasks that
// answer it, then the rows (the window's rows for a date question, else
// the best top rows), ranked so the item that answers the question type
// comes first, cut to maxChars of text in that order.
func build(q question, d data, top, maxChars int) Out {
	if top <= 0 {
		top = DefaultTop
	}
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	out := Out{Query: q.text, When: q.when, Resources: []Resource{}, Tasks: []Task{}, Rows: []Row{}, Ranked: []Ranked{}, Cited: []string{}}
	ms := matchResources(q, d.resources)
	identity := isIdentity(q.text) && len(ms) > 0
	for _, m := range ms {
		out.Resources = append(out.Resources, resourceOf(m, d.resources, q.owe != oweNone))
	}
	if !identity {
		out.Tasks = tasksFor(q, ms, d.tasks)
	}
	lead := 0
	switch {
	case q.owe != oweNone || identity:
		// a debt question is answered by the tasks, "who is X" by the
		// resource: rows would only make the answer heavier
	case !q.window.IsZero():
		out.Rows = windowRows(q, ms, d.rows)
	case q.file != "":
		// a change to a file: its context says what applies; a row is
		// added only when it holds two or more of the question's words
		// (one shared word, such as "invoice", is not about the change)
		rows, _ := topRows(q, ms, d.rows, top)
		out.Rows = slices.DeleteFunc(rows, func(r Row) bool { return len(r.Matched) < 2 })
		lead = len(out.Rows)
	default:
		out.Rows, lead = topRows(q, ms, d.rows, top)
	}
	names := nameIndex(d.resources)
	for i := range out.Rows {
		r := &out.Rows[i]
		r.readTitle, r.readBody = readable(r.Title, names), readable(r.Body, names)
	}
	if leadOf(q, identity, &out) == leadAbout {
		out.lead = lead
	}
	out.cap(out.rank(q, identity, lead), maxChars)
	for _, r := range out.Ranked {
		out.Cited = append(out.Cited, r.ID)
	}
	return out
}

// nameIndex maps every way a ${res:…} reference names a resource of rs
// (its id, its name or an alias, each also as <kind>/<name>), in lower
// case, to the resource's name.
func nameIndex(rs []resourceRow) map[string]string {
	out := make(map[string]string, 3*len(rs))
	for i := range rs {
		r := &rs[i]
		out[strings.ToLower(r.ID)] = r.Name
		for _, n := range r.names() {
			out[n] = r.Name
			out[strings.ToLower(r.Kind)+"/"+n] = r.Name
		}
	}
	return out
}

// readable is text with each ${res:…} reference that names a resource of
// names replaced by the resource's name; any other reference stays as
// written (refs.Readable).
func readable(text string, names map[string]string) string {
	return refs.Readable(text, func(r refs.Ref) (string, bool) {
		if r.Scheme != refs.SchemeRes {
			return "", false
		}
		n, ok := names[strings.ToLower(r.Target)]
		return n, ok
	})
}

// answerFirst is which list leads an answer.
type answerFirst int

const (
	leadResources answerFirst = iota // who is X, or nothing better
	leadTasks                        // what do I owe X, what does X owe me
	leadRows                         // a status, a decision, a budget, a date
	leadAbout                        // everything about X
)

// leadOf is which list answers q first. A debt question is answered by
// its owed tasks, a date question by the rows of its dates, a question
// asking for something the rows hold (a status, a decision, a budget) by
// those rows, everything about X by the resource itself; the resource the
// question names follows as context. Why: an agent reads the first item
// and often stops there (recall gate, top-1 bar), so the project's field
// list must not stand where its status update is the answer.
func leadOf(q question, identity bool, o *Out) answerFirst {
	switch {
	case identity || len(o.Resources) > 0 && isAbout(q.text):
		if !identity {
			return leadAbout
		}
		return leadResources
	case q.owe != oweNone && len(o.Tasks) > 0:
		return leadTasks
	case !q.window.IsZero() && len(o.Rows) > 0:
		return leadRows
	case len(o.Rows) > 0 && len(o.Rows[0].Matched) > 0 && o.Rows[0].History == "":
		// the best row holds what the question asks for, not only the name
		return leadRows
	}
	return leadResources
}

// rank is the answer's reading order (see leadOf). For everything about
// X: the resource, then the lead rows (the newest row of each folder:
// the last meeting, the last journal day), then the open tasks, then the
// other rows; lead is how many of o.Rows are those lead rows.
func (o *Out) rank(q question, identity bool, lead int) []Ranked {
	res := make([]Ranked, 0, len(o.Resources))
	for _, r := range o.Resources {
		res = append(res, Ranked{Kind: KindResource, ID: r.ID})
	}
	tsk := make([]Ranked, 0, len(o.Tasks))
	for _, t := range o.Tasks {
		tsk = append(tsk, Ranked{Kind: KindTask, ID: t.ID})
	}
	rows := make([]Ranked, 0, len(o.Rows))
	for _, r := range o.Rows {
		rows = append(rows, Ranked{Kind: KindRow, ID: r.ID})
	}
	switch leadOf(q, identity, o) {
	case leadTasks:
		return slices.Concat(tsk, res, rows)
	case leadRows:
		return slices.Concat(rows, res, tsk)
	case leadAbout:
		lead = min(lead, len(rows))
		return slices.Concat(res, rows[:lead], tsk, rows[lead:])
	}
	return slices.Concat(res, tsk, rows)
}

// isAbout reports an "everything about X" question: the resource leads,
// then the newest rows that name it, then its open tasks.
func isAbout(text string) bool {
	low := " " + strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{" everything about ", " all about ", " tell me about ", " know about ", " anything about ", " everything on "} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
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
	out := Resource{ID: r.ID, Kind: r.Kind, Name: r.Name, Place: r.Place, Match: m.how, Aliases: r.Aliases, Fields: []Field{}, Linked: []Linked{}}
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
	// a field that names it by its name or an alias links (company:
	// Acme for acme-co with the alias Acme)
	names := []string{strings.ToLower(r.Name)}
	for _, a := range r.Aliases {
		names = append(names, strings.ToLower(strings.TrimSpace(a)))
	}
	for i := range all {
		o := &all[i]
		if o.ID == r.ID {
			continue
		}
		for _, f := range o.Fields {
			if !slices.Contains(names, strings.ToLower(strings.TrimSpace(f.Value))) {
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
		for _, n := range m.res.names() { // owed_to Bobby is owed to robert-kim
			names[n] = true
		}
		ids[m.res.ID] = true
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
		if slices.Contains(r.Refs, m.res.ID) {
			return true
		}
		for _, n := range m.res.names() {
			if allWords(toks, n) {
				return true
			}
		}
	}
	return false
}

// allWords reports whether every word of name (one, or an alias's
// several) is in toks.
func allWords(toks map[string]bool, name string) bool {
	ws := strings.Fields(name)
	for _, w := range ws {
		if !toks[w] {
			return false
		}
	}
	return len(ws) > 0
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
	if r.SupersededBy != "" {
		s.score -= 3
	}
	return s, true
}

// namesResource reports whether word w is (a typo of) a matched name.
func namesResource(w string, ms []match) bool {
	for _, m := range ms {
		for i, n := range m.res.names() {
			// an alias (names()[1:]) is a typo only from four letters on:
			// cto is not the alias CFO
			if w == n || ((i == 0 || len([]rune(w)) >= 4) && osa(w, n) <= typoBudget(w)) {
				return true
			}
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
// meeting note about the same person). Those picks stay first, newest
// first, and lead says how many there are: a newer row of a folder already
// shown comes after the newest row of every other folder.
func topRows(q question, ms []match, rows []textRow, top int) ([]Row, int) {
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
		return []Row{}, 0
	}
	if len(all[0].matched) > 0 {
		// some rows hold what the question asks (a topic, a word): rows
		// that only name the resource would pad the answer
		all = slices.DeleteFunc(all, func(s scored) bool { return len(s.matched) == 0 })
	}
	for i := range all {
		all[i].history = staleNote(all[i].row, q.now)
	}
	markReplaced(all)
	// history (superseded, expired, replaced) never stands where the
	// current answer goes: the current rows are picked first, history
	// only fills what is left of top, marked (devco D-820)
	var hist []scored
	cur := slices.DeleteFunc(slices.Clone(all), func(s scored) bool { return s.history != "" })
	for _, s := range all {
		if s.history != "" {
			hist = append(hist, s)
		}
	}
	all = cur
	if len(all) == 0 {
		all, hist = hist, nil
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
	lead := len(pick)
	for _, s := range all {
		if len(pick) >= top {
			break
		}
		if !taken[s.row.ID] {
			taken[s.row.ID] = true
			pick = append(pick, s)
		}
	}
	byScoreThenNewest := func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return b.row.At.Compare(a.row.At)
	}
	slices.SortStableFunc(pick[:lead], byScoreThenNewest)
	slices.SortStableFunc(pick[lead:], byScoreThenNewest)
	for _, s := range hist {
		if len(pick) >= top {
			break
		}
		pick = append(pick, s)
	}
	out := make([]Row, 0, len(pick))
	for _, s := range pick {
		r := rowOf(s.row, s.matched)
		r.History = s.history
		// a fact that changed: the newest row on the same thing says
		// which of the rows shown with it it replaces (the newest of them)
		var last *textRow
		for _, h := range pick {
			if h.replacedBy == s.row.ID && (last == nil || h.row.At.After(last.At)) {
				last = h.row
			}
		}
		if last != nil {
			r.Note = fmt.Sprintf("newest: it replaces %s from %s", last.ID, last.At.UTC().Format(dates.DayLayout))
		}
		out = append(out, r)
	}
	return out, lead
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
		row := rowOf(r, nil)
		row.History = staleNote(r, q.now) // what happened that day stays, marked when no longer true
		out = append(out, row)
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

// cap keeps the answer within maxChars of text, walking order (the
// reading order): items are kept whole, and the first one that does not
// fit ends the answer, so what is left out is always what ranks lowest;
// Left counts it. It sets Resources, Tasks, Rows and Ranked to what is
// kept.
func (o *Out) cap(order []Ranked, maxChars int) {
	o.maxChars = maxChars
	o.Ranked = []Ranked{}
	for i, r := range order {
		o.Ranked = append(o.Ranked, r)
		if len(o.body()) > maxChars {
			o.Ranked = o.Ranked[:len(o.Ranked)-1]
			o.Left = len(order) - i
			break
		}
	}
	kept := map[Ranked]bool{}
	for _, r := range o.Ranked {
		kept[r] = true
	}
	o.Resources = slices.DeleteFunc(o.Resources, func(r Resource) bool { return !kept[Ranked{KindResource, r.ID}] })
	o.Tasks = slices.DeleteFunc(o.Tasks, func(t Task) bool { return !kept[Ranked{KindTask, t.ID}] })
	o.Rows = slices.DeleteFunc(o.Rows, func(r Row) bool { return !kept[Ranked{KindRow, r.ID}] })
}

// NoAnswer is the line an answer that holds nothing prints.
const NoAnswer = "(no answer)"

// Text is the answer as an agent reads it: the question, the items in
// reading order (resources, open tasks, rows in full, grouped where they
// follow each other), then the ids it cites.

func (o Out) Text() string {
	var b strings.Builder
	body := o.body()
	if o.File != "" && (o.Context != "" || o.note != "") {
		// the file's rules and hotfixes come before the ranked items
		h := o.header()
		f := "\napplies to " + o.File + " (lore context --for-file):\n"
		if o.Context != "" {
			f += o.Context + "\n"
		}
		if o.note != "" {
			f += o.note + "\n"
		}
		body = h + f + strings.TrimPrefix(body, h)
	}
	b.WriteString(body)
	if len(o.Ranked)+len(o.Applies) == 0 {
		// devco D-821: the empty line every empty read prints; the
		// composition root adds what was searched and what to try under
		// it (wire/emptysearch.go), as for every search
		b.WriteString(NoAnswer + "\n")
	}
	if o.Left > 0 {
		fmt.Fprintf(&b, "\n%s left out to stay under %d characters; --max-chars %d prints more\n", plural.Count(o.Left, "more item"), o.maxChars, o.maxChars*2)
	}
	if len(o.Cited) > 0 {
		b.WriteString("\ncited: " + strings.Join(o.Cited, " ") + "\n")
	}
	return b.String()
}

// body is the header and the items of o.Ranked in that order: a run of
// tasks under one "open tasks" heading, a run of rows under one rows
// heading (a second run of rows, after the tasks of an everything-about
// answer, under "more rows").
func (o Out) body() string {
	res, tsk, rows := map[string]Resource{}, map[string]Task{}, map[string]Row{}
	for _, r := range o.Resources {
		res[r.ID] = r
	}
	for _, t := range o.Tasks {
		tsk[t.ID] = t
	}
	for _, r := range o.Rows {
		rows[r.ID] = r
	}
	var b strings.Builder
	b.WriteString(o.header())
	runs := 0 // how many runs of rows the answer has
	for i, it := range o.Ranked {
		if it.Kind == KindRow && (i == 0 || o.Ranked[i-1].Kind != KindRow) {
			runs++
		}
	}
	rowRuns, n := 0, 0
	for i, it := range o.Ranked {
		first := i == 0 || o.Ranked[i-1].Kind != it.Kind
		switch it.Kind {
		case KindResource:
			b.WriteString(res[it.ID].text())
		case KindTask:
			if first {
				run := 0
				for _, x := range o.Ranked[i:] {
					if x.Kind != KindTask {
						break
					}
					run++
				}
				fmt.Fprintf(&b, "\nopen tasks (%d):\n", run)
			}
			b.WriteString(tsk[it.ID].text())
		case KindRow:
			compact := n < o.lead
			if first || n == o.lead && o.lead > 0 {
				b.WriteString(o.rowsHeader(rowRuns, runs, compact))
				rowRuns++
			}
			if compact {
				b.WriteString(rows[it.ID].line())
			} else {
				b.WriteString(rows[it.ID].text(n - o.lead))
			}
			n++
		}
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

// rowsHeader heads the run'th of runs runs of rows (0 the first), or the
// first row after an everything-about answer's newest rows: those newest
// rows (compact) one line each, then the other rows after its open tasks.
func (o Out) rowsHeader(run, runs int, compact bool) string {
	switch {
	case o.When != "":
		return "\nrows dated " + o.When + ", oldest first:\n"
	case compact:
		return "\nnewest row of each folder, one line each:\n"
	case run > 0 || o.lead > 0:
		return "\nmore rows, in full:\n"
	case runs > 1:
		return "\nnewest row of each folder, in full:\n"
	}
	return "\nbest rows, in full:\n"
}

// text is one resource: its line, its fields and what links to it.
func (r Resource) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s %s  %s  (%s)\n", r.Kind, r.Name, r.ID, r.Place)
	if len(r.Aliases) > 0 {
		fmt.Fprintf(&b, "  aliases: %s\n", strings.Join(r.Aliases, ", "))
	}
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

// text is one row in full, numbered i+1, its references read as names.
func (r Row) text(i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%d. %s  %s %s  (%s)", i+1, r.At, r.Table, r.ID, r.Place)
	if r.Note != "" {
		b.WriteString("  " + r.Note)
	}
	if r.History != "" {
		b.WriteString("  [" + r.History + "]")
	}
	b.WriteString("\n")
	title, body := r.readable()
	if title != "" && title != body {
		b.WriteString("   " + title + "\n")
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString(strings.TrimRight("   "+line, " ") + "\n")
	}
	return b.String()
}

// line is one row as an everything-about answer's newest rows print it:
// its day, its folder and its text (title, then the body's first line) on
// one line, the body's other lines indented under it, then its id. Why:
// a small model summarising "everything about X" keeps a one-line fact
// whole and drops the sentence around a name in a block of text (devco
// D-822: "watched the match after lunch" was lost from "Saw X about Y").
func (r Row) line() string {
	title, body := r.readable()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	head := strings.TrimSpace(lines[0])
	if title != "" && title != body {
		head = title + ": " + head
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  - %s (%s): %s", r.At, r.Place, head)
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			b.WriteString("\n      " + l)
		}
	}
	fmt.Fprintf(&b, "  [%s %s]", r.Table, r.ID)
	if r.Note != "" {
		b.WriteString("  " + r.Note)
	}
	if r.History != "" {
		b.WriteString("  [" + r.History + "]")
	}
	return b.String() + "\n"
}

// readable is r's title and body as the text answer prints them: the
// stored text with its resource references read as names when build
// filled them, else the stored text.
func (r Row) readable() (string, string) {
	return cmp.Or(r.readTitle, r.Title), cmp.Or(r.readBody, r.Body)
}
