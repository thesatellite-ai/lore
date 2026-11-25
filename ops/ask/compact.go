package ask

import (
	"fmt"
	"strings"
)

// Long rows: a row longer than longRow characters is printed as the part
// of it that answers the question, at most longRoom characters, never
// whole: a markdown row as its best sections by heading, any other as its
// best paragraph (chunkLong); it is ranked by its best section too
// (longsec.go).
//
// Why (devco D-892, pick list item 6): a design doc or weekly notes of
// 2-10 KB fit under the 4,000-character cap and were printed whole, so
// one long note filled the answer while its one matching section was
// what the agent needed (long-note bench: 128 of 152 questions answered
// at 1,847 characters with the best sections, against 17,288 for whole
// notes). The recall gate's rows are all shorter than longRow, so short
// notes print exactly as before.
const (
	longRow  = 1500
	longRoom = 1000
)

// chunkLong replaces the body of every row of o longer than longRow by
// the part of it that answers the question (cutBody, at most longRoom
// characters): a row with markdown headings by its best sections
// (bestSections: each after a "§ <heading path>" line, best first, the
// first marked "(matching section)"), any other row, or one whose
// sections hold no word of the question the others do not, by its best
// paragraph with "…" where text was left out. It sets FullChars to the
// whole body's length, so the answer says lore show --id <id> prints it
// whole. A date question keeps whole rows: the rows of its dates are the
// answer.
//
// Why a long row is never printed whole (devco D-969, bug hunt H-228,
// S5-04): an architecture note of ~3,900 characters whose sections all
// repeated the question's words stayed whole and was the only row of the
// 4,000-character answer; the memory that answered was left out.
//
// It only marks the answer: cap cuts each long row when it reaches it
// (chunkRow), and leaves out one it reaches with less than minCut of room,
// which no cut could print. Why lazy (devco D-984): cutting all ten rows
// before cap took ~10 ms of a warm ask once search's order put more long
// notes among them, while at most three or four fit the cap.
func (o *Out) chunkLong() {
	o.chunk = o.When == "" || o.prior
}

// unchunked reports whether row i is a long row chunkLong left for cap to
// cut.
func (o *Out) unchunked(i int) bool {
	r := &o.Rows[i]
	return o.chunk && len(r.Body) > longRow && r.FullChars == 0
}

// chunkRow cuts long row i to its best sections (chunkLong).
func (o *Out) chunkRow(i int) {
	if !o.unchunked(i) {
		return
	}
	r := &o.Rows[i]
	words := cutWords(r.Matched, o.asked)
	body, _ := cutBody(r.Body, words, longRoom, r.subject)
	read := ""
	switch r.readBody {
	case "":
	case r.Body:
		read = body // no reference to name: the same text, cut once (D-984)
	default:
		read, _ = cutBody(r.readBody, words, longRoom, r.subject)
	}
	r.FullChars, r.Body, r.readBody = len(r.Body), body, read
	r.LeftOut = leftOutOf(o.q, r.Matched, r.Title, r.Body, r.Comments) // leftout.go
}

// compactBody is body() in its compact form (devco D-892, pick list item
// 6, "level 1"): one line per row ("- <day> <folder>: <text>"), its id
// only on the answer's cited line (every id stays there, in reading
// order), no "best rows" heading or blank lines between items, the
// folder said once above the rows when they all share it, the day left
// out when the answer's one date says it, a changed fact's rows folded
// into its "now:" line, and a resource under rows on one line. A row cut
// to its best sections keeps its lines under its line, then says how to
// read it whole. A resource with no row under it is the answer and keeps
// its full form. --json is unchanged.
//
// Why: on the recall gate the same 70 answers (70/70 correct, top-1 and
// recall@3 unchanged) took about a third fewer characters; the per-row
// id, table and headings were read by every agent and used by none (the
// cited line and --json keep the ids).
func (o Out) compactBody(res map[string]Resource, tsk map[string]Task, rows map[string]Row) string {
	var b strings.Builder
	b.WriteString(o.header())
	now := o.nowLine(rows)
	// the rows the now line already states: the newest and the one it
	// replaces
	folded := map[string]bool{}
	if now != "" {
		for _, it := range o.Ranked {
			if r, ok := rows[it.ID]; ok && it.Kind == KindRow && r.replaces != "" {
				folded[r.ID] = true
				if old, ok := rows[r.replaces]; ok {
					folded[old.ID] = true
					if old.History != "" {
						// its "before that" part is history: say so, with
						// the row that replaced it (devco D-820)
						now = strings.TrimSuffix(now, "\n") + "  [" + old.History + "]\n"
					}
				}
				break
			}
		}
	}
	b.WriteString(now)
	places := map[string]bool{}
	nrows := 0
	for _, it := range o.Ranked {
		if it.Kind == KindRow && !folded[it.ID] {
			places[rows[it.ID].Place] = true
			nrows++
		}
	}
	one := ""
	if len(places) == 1 && nrows > 1 {
		for p := range places {
			one = p
		}
		b.WriteString(one + ":\n")
	}
	oneDay := o.When != "" && !o.prior && !strings.Contains(o.When, " to ")
	for i, it := range o.Ranked {
		switch it.Kind {
		case KindResource:
			if nrows+len(folded) == 0 {
				b.WriteString(strings.TrimPrefix(res[it.ID].text(), "\n"))
				continue
			}
			if r := res[it.ID]; r.touched {
				b.WriteString(r.brief())
				continue
			}
			b.WriteString(res[it.ID].line())
		case KindTask:
			if i == 0 || o.Ranked[i-1].Kind != KindTask {
				b.WriteString("open tasks:\n")
			}
			b.WriteString(tsk[it.ID].line(false))
		case KindRow:
			if folded[it.ID] {
				continue
			}
			b.WriteString(rows[it.ID].line(one == "", oneDay))
		}
	}
	return b.String()
}

// line is one row in the compact answer: its day (unless the answer's one
// date says it), its folder (unless the line above the rows says it), its
// text word for word on one line, and its history when it is no longer
// the current answer. A row cut to its best sections keeps its lines (the
// § heading paths) under the line, then the note that names lore show
// --id <id>.
func (r Row) line(place, oneDay bool) string {
	var b strings.Builder
	b.WriteString("-")
	if !oneDay && r.At != "" {
		b.WriteString(" " + r.At)
	}
	if place {
		b.WriteString(" " + r.Place)
	}
	b.WriteString(":")
	if r.FullChars > 0 {
		title, body := r.readable()
		if title != "" && title != body {
			b.WriteString(" " + title)
		}
		r.historyMark(&b)
		b.WriteString("\n")
		for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			b.WriteString(strings.TrimRight("   "+l, " ") + "\n")
		}
		fmt.Fprintf(&b, "   (%s; lore show --id %s prints it whole)\n", r.cutNote(), r.ID)
		return b.String() + r.commentLines()
	}
	b.WriteString(" " + oneLine(r))
	r.historyMark(&b)
	return b.String() + "\n" + r.commentLines() // D-942: its comments, as history
}

// historyMark adds "  [<history>]" to b when r is no longer the current
// answer, so an agent reads it as what used to be true, and
// "  [stale: …]" when its fact evidence changed after it was verified
// (devco:D-941), so an agent checks it before relying on it.
func (r Row) historyMark(b *strings.Builder) {
	if r.History != "" {
		b.WriteString("  [" + r.History + "]")
	}
	if r.Stale != "" {
		b.WriteString("  [" + r.Stale + "]")
	}
}

// line is a resource under rows in the compact answer: its kind, name and
// field values on one line (a changed value with what it was); a resource
// with no field value is its kind and name.
func (r Resource) line() string {
	var b strings.Builder
	b.WriteString(r.Kind + " " + r.Name)
	sep := ": "
	for _, f := range r.Fields {
		if f.Value == "" {
			continue
		}
		b.WriteString(sep + f.Name + " " + f.Value)
		if f.Was != "" {
			b.WriteString(" (was " + f.Was + ")")
		}
		sep = ", "
	}
	return b.String() + "\n"
}

// briefChars caps the purpose text of a brief resource line.
const briefChars = 100

// briefFields are the fields a brief resource line says what it is by,
// the first one set wins.
var briefFields = []string{"purpose", "description", "summary", "role", "title"}

// brief is a resource the question only touches, under rows: one line
// with its kind, name, what it is (its purpose or description, cut to
// briefChars) and its link. Why (devco D-963, Sync benchmark set 4):
// code_repo cards whose purpose and stack repeated the question's words
// printed every field, ~380 characters each, and ten of them filled the
// 4,000-character answer; lore resource show --id prints it whole.
func (r Resource) brief() string {
	var b strings.Builder
	b.WriteString(r.Kind + " " + r.Name)
	what, link := "", ""
	for _, n := range briefFields {
		for _, f := range r.Fields {
			if what == "" && strings.EqualFold(f.Name, n) && f.Value != "" {
				what = f.Value
			}
		}
	}
	for _, f := range r.Fields {
		if link == "" && strings.Contains(f.Value, "://") {
			link = f.Value
		}
	}
	if rs := []rune(what); len(rs) > briefChars {
		what = strings.TrimRight(string(rs[:briefChars]), " ,;") + "…"
	}
	if what != "" {
		b.WriteString(": " + what)
	}
	if link != "" {
		b.WriteString(" (" + link + ")")
	}
	return b.String() + "\n"
}
