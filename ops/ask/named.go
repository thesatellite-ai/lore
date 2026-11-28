package ask

// named.go — a row an answer prints that names another row by id brings
// that row in (devco D-1037, bug hunt H-297, team-store benchmark set 10
// ask b).
//
//	cap + candidates + grow ─▶ the printed rows
//	pullNamed ─▶ each id a printed row's text names (${row:<id>} or the
//	             bare id) that is a live row of the stores read:
//	             holds a word of the question and fits ─▶ printed after
//	                                                     the row naming it
//	             else                               ─▶ candidate 1, with
//	                                                     its best sentence
//
// Why: a note printed in full said "Rules for rejoining: dec_…" and the
// decision it named, which held the answer, was a candidate line or not
// listed at all; the reader saw an id it could not read and answered
// without it. A row naming itself, two rows naming each other and an id
// that is trashed or not in the stores read are skipped quietly; a row
// brought in is read for ids too, each row at most once.

import (
	"slices"
)

// idHexLen is the length of a row id's tail: 32 lowercase hex characters
// after a three-letter prefix and an underscore (model/ids).
const idHexLen = 32

// namedIDs is every row-id-shaped word of text, in order, each once: a
// three-letter lowercase prefix, an underscore and 32 lowercase hex
// characters, not inside a longer word. ${row:<id>} holds one too.
func namedIDs(text string) []string {
	var out []string
	for i := 3; i+idHexLen < len(text); i++ {
		if text[i] != '_' || !isIDHead(text, i-3) || (i > 3 && isWordByte(text[i-4])) {
			continue
		}
		end := i + 1 + idHexLen
		if !allHex(text[i+1:end]) || (end < len(text) && isWordByte(text[end])) {
			continue
		}
		if id := text[i-3 : end]; !slices.Contains(out, id) {
			out = append(out, id)
		}
		i = end - 1
	}
	return out
}

// isIDHead reports whether text[at:at+3] is three lowercase letters.
func isIDHead(text string, at int) bool {
	for _, c := range []byte(text[at : at+3]) {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// allHex reports whether s is all lowercase hex.
func allHex(s string) bool {
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isWordByte reports a byte that continues an identifier.
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// rowLookup finds a row of the stores the call read by id: the rows read
// for the question first (they carry their stale and fact marks; inRows
// true), else every live row (the corpus); nil when the id is no live row.
type rowLookup func(id string) (r *textRow, inRows bool)

// lookupOf is the rowLookup over d: an index built on the first call, so
// an answer whose printed rows name no id builds none.
func lookupOf(d data) rowLookup {
	var rows map[string]*textRow
	var all *corpus
	return func(id string) (*textRow, bool) {
		if rows == nil {
			rows = make(map[string]*textRow, len(d.rows))
			for i := range d.rows {
				if _, dup := rows[d.rows[i].ID]; !dup {
					rows[d.rows[i].ID] = &d.rows[i]
				}
			}
			all = d.corp
		}
		if r := rows[id]; r != nil {
			return r, true
		}
		if all != nil {
			if p, ok := all.at[id]; ok {
				return &all.rows[p], false
			}
		}
		return nil, false
	}
}

// pullNamed brings in the rows the printed rows name (the file's comment):
// walking the printed rows in reading order, and the rows it prints in
// turn, each named row not printed yet is printed right after the row
// naming it when it was read for the question, holds a word of it and the
// answer fits maxChars with it (printAfter). Of the named rows not
// printed, one leads the candidate list: the one holding the most of the
// question's words, the first named among equals (moved there when the
// list held it). Why one: a printed index note naming a dozen rows put
// them all at the list's head and pushed search's next rows, the answer
// among them, off its end (private benchmark: four questions); the other
// ids stay readable in the printed text.
func (o *Out) pullNamed(find rowLookup, maxChars int) {
	if find == nil || len(o.Rows) == 0 {
		return
	}
	printed := map[string]bool{}
	var queue []string // printed rows to read for ids, in reading order
	for _, it := range o.Ranked {
		if it.Kind == KindRow {
			printed[it.ID] = true
			queue = append(queue, it.ID)
		}
	}
	seen := map[string]bool{}   // ids handled once: no loop, no repeat
	pulled := map[string]bool{} // the rows printed here
	var head *Row               // the named row that leads the list
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		at := slices.IndexFunc(o.Rows, func(r Row) bool { return r.ID == from })
		if at < 0 {
			continue
		}
		title, body := o.Rows[at].readable()
		for _, id := range namedIDs(title + "\n" + body) {
			if id == from || printed[id] || seen[id] {
				continue
			}
			seen[id] = true
			found, inRows := find(id)
			if found == nil {
				continue // trashed, archived or in no store read: skipped quietly
			}
			// a copy: tokens() keeps its word set on the row, and a kept
			// corpus is shared by the read server's calls
			cp := *found
			tr := &cp
			r := rowOf(tr, o.heldBy(tr))
			r.History = staleNote(tr, o.q.now)
			r.readTitle, r.readBody = readable(r.Title, o.names), readable(r.Body, o.names)
			r.subject = o.anySubject()
			if inRows && len(r.Matched) > 0 && o.printAfter(from, r, pulled, maxChars) {
				printed[id], pulled[id] = true, true
				queue = append(queue, id)
				continue
			}
			if head == nil || len(r.Matched) > len(head.Matched) {
				head = &r
			}
		}
	}
	if head != nil && !printed[head.ID] {
		o.leadCandidate(*head)
		o.fitList(maxChars)
	}
}

// heldBy is the question's words row r holds, in the question's order.
func (o *Out) heldBy(r *textRow) []string {
	toks := r.tokens()
	held := []string{}
	for _, w := range o.q.words {
		if hasWord(toks, w) {
			held = append(held, w)
		}
	}
	return held
}

// anySubject is the question's subject terms a printed row carries (the
// same for every row of an answer), nil when none does.
func (o *Out) anySubject() *subjectTerms {
	for i := range o.Rows {
		if o.Rows[i].subject != nil {
			return o.Rows[i].subject
		}
	}
	return nil
}

// printAfter prints row r right after the printed row from (never inside
// an everything-about answer's one-line newest rows) and reports whether
// it fits maxChars next to the candidate list cut to at least
// minCandidates lines (the line r itself held is taken out). A long row
// is cut to its best sections first, as cap cuts one. When --top rows are
// printed already, the last full row (never the first row, nor from, nor
// a row printed here) gives r its place and becomes the list's first
// line: a row a printed row names is nearer the answer than search's
// last full row. Only --top makes a row give its place: taking a row's
// place for room lost more answers than it gained on the private
// benchmark (the displaced rows held them). When r does not fit, the
// answer is left as it was.
func (o *Out) printAfter(from string, r Row, pulled map[string]bool, maxChars int) bool {
	atTop := o.top > 0 && o.rowsBefore(len(o.Ranked)) >= o.top
	return o.tryPrint(from, r, pulled, atTop, maxChars)
}

// tryPrint is printAfter, displacing the last full row first when
// displace; on false the answer is as it was.
func (o *Out) tryPrint(from string, r Row, pulled map[string]bool, displace bool, maxChars int) bool {
	was := *o
	was.Rows, was.Ranked, was.Candidates, was.Dropped = slices.Clone(o.Rows), slices.Clone(o.Ranked), slices.Clone(o.Candidates), slices.Clone(o.Dropped)
	undo := func() bool {
		*o = was
		return false
	}
	if displace {
		last := -1
		for i, it := range o.Ranked {
			if it.Kind == KindRow && it.ID != from && !pulled[it.ID] && o.rowsBefore(i) >= max(o.lead, 1) {
				last = i
			}
		}
		if last < 0 {
			return false
		}
		id := o.Ranked[last].ID
		at := slices.IndexFunc(o.Rows, func(x Row) bool { return x.ID == id })
		c := o.rowCandidate(&o.Rows[at])
		if s := o.q.search; s != nil {
			if i := slices.Index(s.ids, id); i >= 0 {
				c.Rank = i + 1
			}
		}
		o.Ranked = slices.Delete(o.Ranked, last, last+1)
		o.Rows = slices.Delete(o.Rows, at, at+1)
		o.Left++
		o.Candidates = slices.Insert(o.Candidates, 0, c)
	}
	pos := slices.IndexFunc(o.Ranked, func(it Ranked) bool { return it.Kind == KindRow && it.ID == from }) + 1
	if o.rowsBefore(pos) < o.lead {
		pos = o.afterRow(o.lead)
	}
	o.Rows = append(o.Rows, r)
	o.Ranked = slices.Insert(o.Ranked, pos, Ranked{Kind: KindRow, ID: r.ID})
	o.chunkRow(len(o.Rows) - 1)
	o.Candidates = slices.DeleteFunc(o.Candidates, func(c Candidate) bool { return c.ID == r.ID })
	o.Dropped = slices.DeleteFunc(o.Dropped, func(d Dropped) bool { return d.ID == r.ID })
	keep := min(minCandidates, len(o.Candidates))
	if len(o.items())+len(candText(o.Candidates[:keep])) > maxChars {
		return undo()
	}
	o.fitList(maxChars)
	return true
}

// rowsBefore is how many rows o.Ranked holds before place pos.
func (o *Out) rowsBefore(pos int) int {
	n := 0
	for _, it := range o.Ranked[:pos] {
		if it.Kind == KindRow {
			n++
		}
	}
	return n
}

// afterRow is the place in o.Ranked right after its n'th row.
func (o *Out) afterRow(n int) int {
	for i, it := range o.Ranked {
		if it.Kind != KindRow {
			continue
		}
		if n--; n == 0 {
			return i + 1
		}
	}
	return len(o.Ranked)
}

// leadCandidate puts row r first in the candidate list, moved there when
// the list holds it (keeping its search rank).
func (o *Out) leadCandidate(r Row) {
	c := o.rowCandidate(&r)
	if i := slices.IndexFunc(o.Candidates, func(x Candidate) bool { return x.ID == r.ID }); i >= 0 {
		c.Rank = o.Candidates[i].Rank
		o.Candidates = slices.Delete(o.Candidates, i, i+1)
	} else if s := o.q.search; s != nil {
		if i := slices.Index(s.ids, r.ID); i >= 0 {
			c.Rank = i + 1
		}
	}
	o.Dropped = slices.DeleteFunc(o.Dropped, func(d Dropped) bool { return d.ID == r.ID })
	o.Candidates = slices.Insert(o.Candidates, 0, c)
}

// fitList cuts the candidate list from its end to what fits maxChars next
// to the items, at most maxCandidates lines.
func (o *Out) fitList(maxChars int) {
	o.Candidates = o.Candidates[:min(len(o.Candidates), maxCandidates)]
	items := len(o.items())
	for len(o.Candidates) > 0 && items+len(candText(o.Candidates))+len(dropText(droppedBefore(o.Dropped, o.Candidates))) > maxChars {
		o.Candidates = o.Candidates[:len(o.Candidates)-1]
	}
	o.Dropped = droppedBefore(o.Dropped, o.Candidates)
}
