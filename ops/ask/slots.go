package ask

// slots.go — which rows take the answer's full slots (devco D-1035, bug
// hunt H-296, team-store benchmark set 10 asks a and c).
//
//	search's order ─▶ long note A (no key word), long note B (no key
//	                  word), short row C (most of the question's words)
//	slotOrder      ─▶ A, C, B: C is printed whole before B takes its
//	                  room; C, A, B when C also holds a key word and at
//	                  most one word fewer than A
//
// Why: ask printed its rows in search's order and grow widened each cut
// row in that order, so a several-KB note sharing the question's common
// words across its sections took the first full slot, and a second long
// note the next, while the short row stating the answer with most of the
// question's words sat in the candidate list (the reader needed a lore
// show). A long note keeps its place only when it holds a key word of
// the question (subject.go), the word few rows of the store hold.

import "slices"

// keyFrac: a question word weighing at least keyFrac of the
// question's heaviest word (BM25 IDF over the store, subjectTerms) is a
// key word (subjectTerms.key): the words that name the asked thing (an
// error, a job, a table, a field), held by a few rows of the store. A
// fixed share of the store does not tell them apart: in a store of
// 4,000 rows the words of one product (widget, order, sync, shop) are each
// held by fewer than a quarter of the rows, the bar subjectTerms.rare
// reads, and a several-KB note holds some of them for nearly every
// question about the product.
const keyFrac = 0.6

// slotOrder is order with the answer's full slots given out as asked
// (devco D-1035), o.Rows following the new order:
//
//   - the first slot: when the first row is weak (weakRow), the first
//     later row holding a key word of the question and nearly as many of
//     its words (firstSlack) takes its place, the weak row right after
//     it;
//   - the next slots: a short row holding most of the question's words
//     (shortMost) moves before the first weak row ranked above it (past
//     the first slot), so it is printed whole before a second or third
//     long note is printed cut or widened.
//
// Every other row keeps its place. It is order itself for a question not
// answered by rows in search's order (a date window, an everything-about
// answer, a file, a store too small to tell key words: subject nil), and
// a row of the answer's now: line (one replacing another, or the one it
// replaces) never moves and is never passed.
func (o *Out) slotOrder(order []Ranked) []Ranked {
	if o.answer != leadRows || o.lead > 0 || o.q.search == nil || o.When != "" || len(o.Rows) < 2 {
		return order
	}
	byID := make(map[string]Row, len(o.Rows))
	chain := map[string]bool{} // the rows the now: line names
	for _, r := range o.Rows {
		byID[r.ID] = r
		if r.replaces != "" {
			chain[r.ID], chain[r.replaces] = true, true
		}
	}
	isRow := func(it Ranked) (Row, bool) {
		r, ok := byID[it.ID]
		return r, ok && it.Kind == KindRow && !chain[it.ID]
	}
	weak := func(it Ranked) bool {
		r, ok := isRow(it)
		return ok && o.weakRow(r)
	}
	out := slices.Clone(order)
	moved := false
	fi := slices.IndexFunc(out, func(it Ranked) bool { return it.Kind == KindRow })
	if fi < 0 {
		return order
	}
	if first, _ := isRow(out[fi]); weak(out[fi]) {
		j := slices.IndexFunc(out[fi+1:], func(it Ranked) bool {
			r, ok := isRow(it)
			return ok && r.subject != nil && o.keyHeld(r) && o.heldWords(r) >= o.heldWords(first)-firstSlack
		})
		if j >= 0 {
			it := out[fi+1+j]
			out = slices.Insert(slices.Delete(out, fi+1+j, fi+2+j), fi, it)
			moved = true
		}
	}
	for i := fi + 2; i < len(out); i++ {
		r, ok := isRow(out[i])
		if !ok || !o.shortMost(r) {
			continue
		}
		at := slices.IndexFunc(out[fi+1:i], weak)
		if at < 0 {
			continue
		}
		it := out[i]
		out = slices.Insert(slices.Delete(out, i, i+1), fi+1+at, it)
		moved = true
	}
	if !moved {
		return order
	}
	place := make(map[string]int, len(out))
	for i, it := range out {
		if it.Kind == KindRow {
			place[it.ID] = i
		}
	}
	slices.SortStableFunc(o.Rows, func(a, b Row) int { return place[a.ID] - place[b.ID] })
	return out
}

// weakRow reports a long row (over shortRow characters, so cap cuts it)
// whose whole text holds no key word of the question: slotOrder moves
// the rows that answer better before it.
func (o *Out) weakRow(r Row) bool {
	return len(r.Body) > shortRow && r.subject != nil && !o.keyHeld(r)
}

// firstSlack is how many fewer of the question's words than the weak
// first row a row holding a key word may hold and still take the first
// slot: one. Why: a word few rows of the store hold is not always the
// subject ("usually", "anyway" can be rare in a store of design notes), and
// on the team-store benchmark's tuning half a row that took the first
// slot holding two or more words fewer answered less than the row it
// replaced.
const firstSlack = 1

// heldWords is how many of the question's words r holds: search's own
// count (searchList.held, a stale row's words by their share) when the
// question has one, else the words r matched.
func (o *Out) heldWords(r Row) float64 {
	if s := o.q.search; s != nil {
		if h, ok := s.held[r.ID]; ok {
			return h
		}
	}
	return float64(len(r.Matched))
}

// shortMost reports a short row (at most shortRow characters, never
// cut) holding most of the question's distinct words (holdsMost).
func (o *Out) shortMost(r Row) bool {
	return r.subject != nil && len(r.Body) <= shortRow && o.holdsMost(r)
}

// keyHeld reports whether r's title or text holds a key word of the
// question (a word r matched that subjectTerms.key holds).
func (o *Out) keyHeld(r Row) bool {
	toks := tokensOf(r.Title + "\n" + r.Body + "\n" + r.readBody)
	for _, w := range r.Matched {
		if r.subject.isKey(w) && (hasWord(toks, w) || identWord(o.q, toks, w)) {
			return true
		}
	}
	return false
}

// holdsMost reports whether r's title or text holds more than half of
// the question's distinct content words, and at least two of them.
func (o *Out) holdsMost(r Row) bool {
	toks := tokensOf(r.Title + "\n" + r.Body + "\n" + r.readBody)
	seen := map[string]bool{}
	n, held := 0, 0
	for _, w := range o.q.words {
		if seen[w] {
			continue
		}
		seen[w] = true
		n++
		if hasWord(toks, w) || identWord(o.q, toks, w) {
			held++
		}
	}
	return held >= 2 && held*2 > n
}
