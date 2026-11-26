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
	text, cut := cutBody(src, words, room, w.subject)
	if !cut {
		return
	}
	if lost := o.dropped(text, was, words); lost != "" {
		text, _ = cutBody(src, words, max(room-len(lost)-len(elision)-2, 1), w.subject)
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
		r.Body, _ = cutBody(w.Body, words, room, w.subject)
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
