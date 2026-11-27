package ask

// grow.go — what ask prints in full is decided by bytes, not by a fixed
// count (devco D-1006, bug hunt H-268).
//
//	cap + candidates ─▶ 1 row cut to 1,000 characters, 7 candidate lines,
//	                    ~1,200 characters of --max-chars unused
//	grow             ─▶ the cut row printed whole when it fits next to
//	                    the first minCandidates lines, else its excerpt
//	                    widened to the room left; then the next rows
//	                    printed whole while they fit, up to maxFull
//
// Why: on a store of long notes cap cut the answering row to its best
// sections (longRoom) and filled the rest with candidate lines, so the
// sentence asked about was often in the row but not on the page and the
// reader needed a lore show (H-245 had cut the full rows to one or two).
// A long row gets a wider slot only when what it printed holds a rare
// word of the question (subject.go): a big note off the subject stays
// cut, as before.

import (
	"cmp"
	"slices"
	"strings"
)

// maxFull is the most rows grow prints whole or widened: enough for a
// two- or three-row answer, few enough that the candidate list stays.
const maxFull = 3

// grow spends what cap and the candidate list left of maxChars on the
// rows: each printed cut row, in order, is printed whole when that fits
// next to the first minCandidates candidate lines, else its excerpt is
// widened to the room left; then the rows cap did not print (all, then
// more) are printed whole while they fit, until maxFull rows (or --top,
// when lower) are printed; a row the list leaves out on purpose (dropped,
// the same text as another row) is never promoted.
// all are the rows before cap cut them, list the candidate list so far
// and dropped the rows candidatesOf left out of it; it returns both for
// the rows still unprinted.
func (o *Out) grow(all, more []Row, cards []Candidate, memo map[string]Candidate, list []Candidate, dropped []Dropped, maxChars int) ([]Candidate, []Dropped) {
	if o.When != "" && !o.prior {
		return list, dropped // a date question prints the window's rows as before
	}
	whole := map[string]Row{}
	for _, rs := range [][]Row{all, more} {
		for _, r := range rs {
			if _, ok := whole[r.ID]; !ok {
				whole[r.ID] = r
			}
		}
	}
	fits := func(l []Candidate) bool {
		return len(o.items())+len(candText(l[:min(minCandidates, len(l))])) <= maxChars
	}
	limit := maxFull
	if o.top > 0 {
		limit = min(limit, o.top) // --top caps the rows printed in full
	}
	rows := 0
	for _, it := range slices.Clone(o.Ranked) {
		if it.Kind != KindRow || rows == limit {
			continue
		}
		i := slices.IndexFunc(o.Rows, func(r Row) bool { return r.ID == it.ID })
		if i < 0 {
			continue
		}
		rows++
		r, w := &o.Rows[i], whole[it.ID]
		if r.FullChars == 0 || w.ID == "" || !o.onSubject(*r) {
			continue
		}
		cut := *r
		*r = w
		if fits(list) {
			continue
		}
		*r = cut
		if rows == 1 && o.lead == 0 {
			// devco D-1011: the first row's room comes before a later
			// row's cut; the later cut rows become candidate lines
			if l, d, ok := o.yieldCuts(it.ID, all, more, cards, memo); ok {
				list, dropped = l, d
				i = slices.IndexFunc(o.Rows, func(r Row) bool { return r.ID == it.ID })
				r = &o.Rows[i]
				*r = w
				if fits(list) {
					continue
				}
				*r = cut
			}
		}
		o.widen(i, w, maxChars-len(o.items())-len(candText(list[:min(minCandidates, len(list))])))
	}
	for _, rs := range [][]Row{all, more} {
		for _, w := range rs {
			if rows >= limit {
				break
			}
			if slices.ContainsFunc(o.Rows, func(r Row) bool { return r.ID == w.ID }) ||
				slices.ContainsFunc(dropped, func(d Dropped) bool { return d.ID == w.ID }) {
				continue // printed, or left out of the list on purpose (same text as another row)
			}
			if len(w.Body) > longRow && !o.onSubject(w) {
				continue // a big note off the subject stays a candidate
			}
			o.Rows = append(o.Rows, w)
			o.Ranked = append(o.Ranked, Ranked{KindRow, w.ID})
			next, nextDropped := o.candidatesOf(all, more, cards, memo)
			if !fits(next) {
				o.Rows = o.Rows[:len(o.Rows)-1]
				o.Ranked = o.Ranked[:len(o.Ranked)-1]
				continue
			}
			o.Left--
			rows++
			list, dropped = next, nextDropped
		}
	}
	return list, dropped
}

// yieldMin is the shortest whole row yieldCuts takes out of the answer:
// a row up to twice longRow is mostly on the page in its cut, and on the
// real benchmark such a second row answered as often as the first row's
// wider cut (realeval TUNE: the two questions it lost were rows of 1,548
// and 1,748 characters; every row it won on was over 3,600).
const yieldMin = 2 * longRow

// yieldCuts takes every printed row after the row first out of the
// answer when cap cut it from a whole body over yieldMin characters (a
// row printed whole, or a short one, stays), each counted as left out and
// listed again as a candidate line, so the room their excerpts held goes
// to first; a row of the answer's now: line (one replacing another, or
// the one it replaces) stays. ok is false when no row is taken out.
// It returns the candidate list and its dropped rows for what is left.
//
// Why (devco D-1011, bug hunt H-273): on a store of long notes cap cut
// the first two rows to about 1,000 characters each next to five
// candidate lines, so the first row, the answering one on most questions,
// was found and printed first but its answering sentence did not fit in
// its excerpt; a second long row's excerpt answered far fewer questions
// than the first row printed whole or widened to the freed room.
func (o *Out) yieldCuts(first string, all, more []Row, cards []Candidate, memo map[string]Candidate) ([]Candidate, []Dropped, bool) {
	chain := map[string]bool{} // the rows the now: line names
	for _, r := range o.Rows {
		if r.replaces != "" {
			chain[r.ID], chain[r.replaces] = true, true
		}
	}
	gone := map[string]bool{}
	for _, r := range o.Rows {
		if r.ID != first && r.FullChars > yieldMin && !chain[r.ID] {
			gone[r.ID] = true
		}
	}
	if len(gone) == 0 {
		return nil, nil, false
	}
	o.Rows = slices.DeleteFunc(o.Rows, func(r Row) bool { return gone[r.ID] })
	o.Ranked = slices.DeleteFunc(o.Ranked, func(it Ranked) bool { return it.Kind == KindRow && gone[it.ID] })
	o.Left += len(gone)
	list, dropped := o.candidatesOf(all, more, cards, memo)
	return list, dropped, true
}

// widen re-cuts row i of o.Rows from its whole form w to at most its
// printed length plus spare bytes, when that adds at least minCut. A
// wider cut can pick other sentences, and the one the narrow cut kept
// may be the answer (realeval: a row's key sentence dropped when its
// room grew), so the lines of the narrow cut the wide one left out
// follow it, after an elision mark, in the room.
func (o *Out) widen(i int, w Row, spare int) {
	if spare < minCut {
		return
	}
	r := &o.Rows[i]
	was := cmp.Or(r.readBody, r.Body)
	words := cutWords(w.Matched, o.asked)
	room := len(was) + spare
	src := cmp.Or(w.readBody, w.Body)
	text, cut := cutKeeping(src, w.Title, words, o.q.words, room, w.subject)
	if !cut {
		return
	}
	if lost := o.dropped(text, was, words); lost != "" {
		text, _ = cutKeeping(src, w.Title, words, o.q.words, max(room-len(lost)-len(elision)-2, 1), w.subject)
		lost = o.dropped(text, was, words)
		if lost == "" {
			return
		}
		text = strings.TrimRight(text, "\n") + "\n" + elision + "\n" + lost
		if len(text) > room || len(text) <= len(was) {
			return
		}
	}
	r.FullChars = cmp.Or(r.FullChars, len(w.Body))
	r.Body = text
	if w.readBody != "" {
		r.readBody = text
	}
	if w.readBody != "" && w.readBody != w.Body {
		// the stored body (--json) keeps its references: cut it the same
		// way, the narrow cut's lost lines left to the read text
		r.Body, _ = cutKeeping(w.Body, w.Title, words, o.q.words, room, w.subject)
	}
	r.LeftOut = leftOutOf(o.q, r.Matched, r.Title, r.Body+"\n"+r.readBody, r.Comments) // leftout.go
}

// dropped is the lines of was (an excerpt) that hold a word of words
// now does not hold, joined by newlines; "" when now holds every word of
// words was holds. Why only those: a line of the narrow cut holding only
// words the wide cut prints too is what the wide cut replaced with
// better ones; a question word only the narrow cut held must stay on the
// page (H-268 ask 1: the cut note named the question's own words).
func (o *Out) dropped(now, was string, words []string) string {
	have := tokensOf(now)
	var lack []string
	for _, w := range words {
		if lw := strings.ToLower(w); !hasWord(have, lw) && !identWord(o.q, have, lw) {
			lack = append(lack, lw)
		}
	}
	if len(lack) == 0 {
		return ""
	}
	var out []string
	for _, l := range strings.Split(was, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || t == elision {
			continue
		}
		toks := tokensOf(t)
		if slices.ContainsFunc(lack, func(w string) bool { return hasWord(toks, w) || identWord(o.q, toks, w) }) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// onSubject reports whether row r is short (at most longRow characters)
// or what it printed holds a rare word of the question (subject.go):
// only such a row gets a wider slot. Among fewer than rareFloor scored
// rows no word is rare and a long row stays cut.
func (o *Out) onSubject(r Row) bool {
	if len(r.Body) <= longRow && r.FullChars <= longRow {
		return true
	}
	if r.subject == nil {
		// fewer than rareFloor scored rows: no word tells the subject
		// apart, so a long row stays cut as before (devco D-969)
		return false
	}
	toks := tokensOf(r.Title + "\n" + cmp.Or(r.readBody, r.Body))
	for _, w := range r.Matched {
		if r.subject.isRare(w) && (hasWord(toks, w) || identWord(o.q, toks, w)) {
			return true
		}
	}
	return false
}
