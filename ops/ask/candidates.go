package ask

// candidates.go — after the full rows, lore ask lists search's next
// candidates one line each, and resource cards stand only there when a
// row answers (bug hunt H-245, devco D-998, owner decision 2026-11-25
// item 3).
//
// Why: an agent that reads an answer whose full rows miss what it asked
// had no way to see what came next but to ask again or search; and
// resource cards (code_repo and other resources) printed in full between
// the rows took the places a row that answers should have (held-out
// set: a card in the first three places on 7 of 51 questions). The list
// names each next item (id, kind, title, the one line of it that holds
// the most words of the question), so the agent runs lore show --id on
// the one it needs, and a card whose purpose answers is still shown.
//
//	ranked items ─▶ cap (full rows)  ─▶ printed in full
//	             └▶ rows cap left out, the next rows of search's order,
//	                cards when a row answers ─▶ candidates (5-7 lines)
//
// The list counts in --max-chars: when the full items and the list do not
// fit together, cap runs again with the room of five lines kept free.

import (
	"bytes"
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The candidate list's size and line widths.
const (
	// minCandidates and maxCandidates bound the list: at most 7 lines,
	// and the full items give up room for at least 5 when there are 5.
	minCandidates = 5
	maxCandidates = 7
	// candTitleMax and candLineMax cap a line's title and matching text
	// (runes); a card's line may run to candCardMax, so a purpose
	// sentence that answers is printed whole.
	candTitleMax = 80
	candLineMax  = 160
	candCardMax  = 240
)

// CandidatesHeading heads the list in the text answer.
const CandidatesHeading = "next candidates (lore show --id <id> prints one in full):"

// Candidate is one item after the answer's full rows: a row search ranks
// next, or a resource card, as one line.
type Candidate struct {
	ID    string `json:"id"`
	Kind  string `json:"kind" desc:"row or resource: the kind of item"`
	Table string `json:"table" desc:"a row's table (memories, decisions …) or a resource's kind (code_repo, person …)"`
	Place string `json:"place" desc:"where it is stored (<store>/<folder>)"`
	Title string `json:"title" desc:"a row's title (else its first line) or a resource's name"`
	Line  string `json:"line" desc:"the sentence of it that best matches the question, the question's rare words weighing most, after its heading (heading › sentence); a resource's field as name: value"`
	// Rank is the row's place in lore search's order for the question
	// (1 = search's first hit; bug hunt H-270), 0 for a card.
	Rank int `json:"rank,omitempty" desc:"a row's place in lore search's order for the question (1 = search's first hit); the list holds search's next rows in that order"`
	// held is how many of the question's words the item holds: a card
	// stands among the rows by it (merge).
	held int
	// named marks a resource the question names (not only touches).
	named bool
}

// Dropped is a row search ranks among the candidates that the list
// leaves out, and why (bug hunt H-270): the list is search's next rows,
// so a row it skips says so instead of going missing.
type Dropped struct {
	ID     string `json:"id"`
	Table  string `json:"table"`
	Rank   int    `json:"rank" desc:"its place in lore search's order for the question"`
	Reason string `json:"dropped_reason" desc:"why the list leaves it out: same text as <id> (the note is stored twice and the other copy is printed or listed)"`
}

// DroppedHeading leads the text answer's line naming the dropped rows.
const DroppedHeading = "  left out of the list:"

// isCard reports whether resource r prints as a candidate line instead of
// a full item: whenever rows are in the answer and the question is not
// "who is X", a card the question only touches goes to the list, and so
// does every card when a row answers the question (leadRows). A who-is,
// everything-about or debt answer keeps the resource it names: there the
// resource is what was asked.
func (o *Out) isCard(r Resource) bool {
	if o.identity || len(o.Rows) == 0 {
		return false
	}
	return r.touched || o.answer == leadRows
}

// splitCards takes the cards (isCard) out of order and o.Resources and
// returns them as candidates.
func (o *Out) splitCards(order []Ranked) ([]Ranked, []Candidate) {
	var cards []Candidate
	moved := map[string]bool{}
	for _, r := range o.Resources {
		if o.isCard(r) {
			moved[r.ID] = true
			cards = append(cards, o.cardCandidate(r))
		}
	}
	if len(cards) == 0 {
		return order, nil
	}
	o.Resources = slices.DeleteFunc(o.Resources, func(r Resource) bool { return moved[r.ID] })
	order = slices.DeleteFunc(order, func(r Ranked) bool { return r.Kind == KindResource && moved[r.ID] })
	return order, cards
}

// fitCandidates caps the answer with the candidate list in the budget:
// cap first; while the full items and the first minCandidates lines do
// not fit maxChars together, the last full item gives up its place (a
// row becomes the list's first line); then the list is cut to what fits,
// at most maxCandidates. more are the rows past --top, cards the
// resource candidates. Why not a second cap with the room kept: cap
// renders the answer once per item, and twice cost 30 ms of the 50 ms
// warm budget; dropping from the end renders it once per dropped item.
func (o *Out) fitCandidates(order []Ranked, maxChars int, more []Row, cards []Candidate) {
	all := slices.Clone(o.Rows) // whole, before cap cuts them
	o.cap(order, maxChars)
	memo := map[string]Candidate{}
	list, dropped := o.candidatesOf(all, more, cards, memo)
	if len(list) == 0 {
		return
	}
	items := len(o.items())
	for len(o.Ranked) > 1 && items+len(candText(list[:min(minCandidates, len(list))])) > maxChars {
		o.dropLast()
		list, dropped = o.candidatesOf(all, more, cards, memo)
		items = len(o.items())
	}
	for len(list) > 0 && items+len(candText(list))+len(dropText(droppedBefore(dropped, list))) > maxChars {
		list = list[:len(list)-1]
	}
	o.Candidates, o.Dropped = list, droppedBefore(dropped, list)
}

// droppedBefore is the dropped rows search ranks before the list's last
// row: a row ranked past the list's end was not dropped, the list ended.
func droppedBefore(dropped []Dropped, list []Candidate) []Dropped {
	last := 0
	for _, c := range list {
		last = max(last, c.Rank)
	}
	out := []Dropped{}
	for _, d := range dropped {
		if d.Rank < last {
			out = append(out, d)
		}
	}
	return out
}

// dropLast takes the last full item out of the answer, counted as left
// out, as cap leaves out an item that does not fit.
func (o *Out) dropLast() {
	it := o.Ranked[len(o.Ranked)-1]
	o.Ranked = o.Ranked[:len(o.Ranked)-1]
	o.Left++
	switch it.Kind {
	case KindRow:
		if i := slices.IndexFunc(o.Rows, func(r Row) bool { return r.ID == it.ID }); i >= 0 {
			if i < o.lead {
				o.lead--
			}
			o.Rows = slices.Delete(o.Rows, i, i+1)
		}
	case KindResource:
		o.Resources = slices.DeleteFunc(o.Resources, func(r Resource) bool { return r.ID == it.ID })
	case KindTask:
		o.Tasks = slices.DeleteFunc(o.Tasks, func(t Task) bool { return t.ID == it.ID })
	}
}

// candidatesOf is the list after cap: search's next rows (the rows of
// search's order cap did not print, plainOrder) when the question has
// one, else the rows of all cap did not print, in their order, then
// more; with the cards placed among them (merge), at most maxCandidates.
// dropped are the rows of search's order the list leaves out, with why.
// memo keeps each row's line across calls.
func (o *Out) candidatesOf(all, more []Row, cards []Candidate, memo map[string]Candidate) ([]Candidate, []Dropped) {
	printed := map[string]bool{}
	for _, r := range o.Ranked {
		if r.Kind == KindRow {
			printed[r.ID] = true
		}
	}
	if s := o.q.search; s != nil && len(s.ids) > 0 {
		rows, dropped := o.searchCandidates(all, more, printed, memo)
		list := merge(rows, cards)
		return list[:min(len(list), maxCandidates)], dropped
	}
	var rows []Candidate
	for _, rs := range [][]Row{all, more} {
		for i := 0; i < len(rs) && len(rows) < maxCandidates; i++ {
			if !printed[rs[i].ID] {
				printed[rs[i].ID] = true
				c, ok := memo[rs[i].ID]
				if !ok {
					c = o.rowCandidate(&rs[i])
					memo[rs[i].ID] = c
				}
				rows = append(rows, c)
			}
		}
	}
	list := merge(rows, cards)
	return list[:min(len(list), maxCandidates)], nil
}

// searchCandidates is search's next rows (o.q.search) cap did not print,
// in search's order, at most maxCandidates, each with its place in it;
// a row whose text is the same as a row printed or listed before it (one
// note stored twice) is dropped, with why. A row ask read in full (all,
// more) keeps its readable text; another is read from search's rows.
func (o *Out) searchCandidates(all, more []Row, printed map[string]bool, memo map[string]Candidate) ([]Candidate, []Dropped) {
	s := o.q.search
	read := map[string]*Row{}
	var subject *subjectTerms
	for _, rs := range [][]Row{all, more} {
		for i := range rs {
			read[rs[i].ID] = &rs[i]
			subject = cmp.Or(subject, rs[i].subject)
		}
	}
	seen := map[uint64]string{} // a row's text → the first row holding it
	for _, r := range all {
		if printed[r.ID] {
			seen[sameKey(r)] = r.ID
		}
	}
	var rows []Candidate
	var dropped []Dropped
	for i, id := range s.ids {
		if len(rows) >= maxCandidates {
			break
		}
		if printed[id] {
			continue
		}
		r := read[id]
		if r == nil {
			tr := s.rows[id]
			if tr == nil {
				continue
			}
			one := rowOf(tr, nil)
			one.readTitle, one.readBody = readable(one.Title, o.names), readable(one.Body, o.names)
			one.subject = subject
			r = &one
		}
		if first, ok := seen[sameKey(*r)]; ok {
			dropped = append(dropped, Dropped{ID: id, Table: r.Table, Rank: i + 1, Reason: "same text as " + first})
			continue
		}
		seen[sameKey(*r)] = id
		c, ok := memo[id]
		if !ok {
			c = o.rowCandidate(r)
			memo[id] = c
		}
		c.Rank = i + 1
		rows = append(rows, c)
	}
	return rows, dropped
}

// sameKey is what makes two rows the same note: the hash of their title
// and whole text (all and the rows read from search's list hold it
// before cap cuts it).
func sameKey(r Row) uint64 {
	return textHash(r.Title + "\x00" + r.Body)
}

// dropText is the text answer's line naming the dropped rows.
func dropText(ds []Dropped) string {
	if len(ds) == 0 {
		return ""
	}
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.ID + " (" + d.Reason + ")"
	}
	return DroppedHeading + " " + strings.Join(parts, ", ") + "\n"
}

// merge places the cards among the rows (both in their own order): a card
// the question names first, else a card before the first row that holds
// fewer of the question's words, so a card stands where the words it
// shares with the question put it, as search's word count does.
func merge(rows, cards []Candidate) []Candidate {
	out := make([]Candidate, 0, len(rows)+len(cards))
	for _, c := range cards {
		if c.named {
			out = append(out, c)
		}
	}
	i := 0
	for _, c := range cards {
		if c.named {
			continue
		}
		for i < len(rows) && rows[i].held >= c.held {
			out = append(out, rows[i])
			i++
		}
		out = append(out, c)
	}
	return append(out, rows[i:]...)
}

// rowCandidate is row r as a candidate line.
func (o *Out) rowCandidate(r *Row) Candidate {
	title, body := r.readable()
	title = cmp.Or(strings.TrimSpace(title), firstLine(body))
	line, held := matchLine(body, title, o.q.words, r.subject, candLineMax)
	return Candidate{ID: r.ID, Kind: KindRow, Table: r.Table, Place: r.Place, Title: clip(title, candTitleMax), Line: line, held: held}
}

// cardCandidate is resource r as a candidate line: its field holding the
// most words of the question; among equals its purpose or description
// (what the brief card line printed, devco D-963), else the first.
func (o *Out) cardCandidate(r Resource) Candidate {
	c := Candidate{ID: r.ID, Kind: KindResource, Table: r.Kind, Place: r.Place, Title: clip(r.Name, candTitleMax), named: !r.touched}
	best, field := -1, ""
	for _, f := range r.Fields {
		if f.Value == "" || isContact(f.Value) {
			continue
		}
		line, held := bestLine(f.Value, o.q.words, candCardMax-len(f.Name)-2)
		held += heldBy(f.Name, o.q.words) // "budget" asks for the budget field
		if held > best || held == best && says(f.Name) && !says(field) {
			best, field, c.Line, c.held = held, f.Name, f.Name+": "+line, held
		}
	}
	c.held = max(c.held, r.parts)
	return c
}

// says reports a field that says what a resource is.
func says(field string) bool { return field == "purpose" || field == "description" }

// bestLine is the sentence of text holding the most distinct words of
// words (the first of equals; the first sentence when none holds one),
// clipped to width runes around its first held word, and how many words
// it holds.
func bestLine(text string, words []string, width int) (string, int) {
	best, held := "", -1
	for _, s := range sentencesOf(text) {
		t := strings.TrimSpace(s.text)
		if t == "" || fenceOf(t) != "" {
			continue
		}
		if best == "" {
			best, held = t, 0
		}
		if !mayHold(asciiLower(t), words) {
			continue
		}
		toks := tokensOf(t)
		n := 0
		for _, w := range words {
			if hasWord(toks, w) {
				n++
			}
		}
		if n > held {
			best, held = t, n
		}
	}
	return clipAround(best, words, width), max(held, 0)
}

// matchLine is the sentence of a row's text that best matches words, as
// an excerpt picks its first sentence (sentenceScores' weights, cutWeight:
// each word by its rarity within the row and across the store, an
// identifier more), after
// the heading it sits under when that is not the title ("Heading ›
// sentence"), clipped to width runes around its first held word; and
// the most distinct words one sentence of it holds. Without a sentence telling the words
// apart it is bestLine's. Why (bug hunt H-270): the line held the most
// question words, so a sentence repeating the common ones ("the sync
// job runs …") stood for a row whose answer was the sentence naming the
// rare one, and the agent skipped the row the answer was in.
func matchLine(text, title string, words []string, st *subjectTerms, width int) (string, int) {
	// every sentence counts in the row's IDF; only the ones that may hold
	// a word (mayHold) are tokenized, as bestLine does: a long note is
	// read once per candidate line within ask's 50 ms
	n := 0
	var ss []sentence
	for _, s := range sentencesOf(text) {
		t := strings.TrimSpace(s.text)
		if t == "" || fenceOf(t) != "" {
			continue
		}
		n++
		if mayHold(asciiLower(t), words) {
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 || len(words) == 0 {
		return bestLine(text, words, width)
	}
	toks := make([]map[string]bool, len(ss))
	for i, s := range ss {
		toks[i] = tokensOf(s.text)
	}
	idents := identWords(text, words)
	score, per := make([]float64, len(ss)), make([]int, len(ss))
	held := 0 // the most words one sentence holds, as bestLine counts: merge places a card by it
	for _, w := range words {
		w = strings.ToLower(w)
		var hold []int
		for i := range ss {
			if hasWord(toks[i], w) {
				hold = append(hold, i)
			}
		}
		for _, i := range hold {
			per[i]++
			held = max(held, per[i])
		}
		if len(hold) == 0 || len(hold) == n && n > 1 {
			continue // a word every sentence holds tells none apart (sentenceScores)
		}
		wt := cutWeight(n, len(hold), w, st, idents[w])
		for _, i := range hold {
			score[i] += wt
		}
	}
	best := -1
	for i, sc := range score {
		if sc > 0 && (best < 0 || sc > score[best]) {
			best = i
		}
	}
	if best < 0 {
		return bestLine(text, words, width)
	}
	s := strings.TrimSpace(ss[best].text)
	head := ""
	if n := len(ss[best].heads); n > 0 {
		head = strings.TrimSpace(strings.TrimLeft(ss[best].heads[n-1], "#"))
		if strings.EqualFold(head, strings.TrimSpace(title)) {
			head = ""
		}
	}
	if head == "" {
		return clipAround(s, words, width), held
	}
	head = clip(head, width/3)
	return head + " › " + clipAround(s, words, width-utf8.RuneCountInString(head)-3), held
}

// clip cuts s to width runes, … marking the cut.
func clip(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:width-1])) + "…"
}

// clipAround cuts s to width runes keeping its first word of words in
// view: the cut starts a little before that word when it lies past half
// the width.
func clipAround(s string, words []string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	low := string(asciiLower(s)) // same byte offsets as s
	at := -1
	for _, w := range words {
		if i := strings.Index(low, w); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	start := 0
	if at >= 0 {
		if ra := utf8.RuneCountInString(s[:at]); ra > width/2 {
			start = max(0, min(ra-width/4, len(r)-width+1))
		}
	}
	out := string(r[start:min(len(r), start+width-1)])
	if start > 0 {
		out = "…" + strings.TrimSpace(out)
	}
	return strings.TrimSpace(out) + "…"
}

// candText is the list as the text answer prints it: the heading, then
// one line per candidate: id (table) title: line.
func candText(list []Candidate) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	// no blank line before it: the compact answer has none (devco D-892)
	b.WriteString(CandidatesHeading + "\n")
	for _, c := range list {
		b.WriteString("  " + c.ID + " (" + c.Table + ") ")
		switch {
		case c.Line == "":
			b.WriteString(c.Title)
		case strings.HasPrefix(c.Line, strings.TrimSuffix(c.Title, "…")):
			// a row without a title: its title is its first line
			b.WriteString(c.Line)
		default:
			b.WriteString(c.Title + ": " + c.Line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// pastTop splits rows (picked with room for the list) at top: the rows
// printed in full and the rows past them, which only the candidate list
// names; lead is kept within top.
func pastTop(rows []Row, top, lead int) ([]Row, []Row, int) {
	if len(rows) <= top {
		return rows, nil, lead
	}
	return rows[:top:top], slices.Clone(rows[top:]), min(lead, top)
}

// mayHold reports whether low (lower case) holds the first four letters
// of a word of words (the word whole when shorter): a sentence without
// is not tokenized, the forms hasWord finds share that start.
func mayHold(low []byte, words []string) bool {
	for _, w := range words {
		if bytes.Contains(low, []byte(w[:min(4, len(w))])) {
			return true
		}
	}
	return false
}

// heldBy is how many of words name holds (a field's name).
func heldBy(name string, words []string) int {
	toks, n := tokensOf(name), 0
	for _, w := range words {
		if hasWord(toks, w) {
			n++
		}
	}
	return n
}
