// Package staleclaim finds a row whose "planned / not yet" claim a newer
// row contradicts (devco:D-1027, bug hunt H-289): an older note says the
// retry queue is planned, a newer note says the retry queue shipped. Both
// stay live, and an agent reading the older one first answered "not built
// yet". factstale flags a row whose code evidence drifted; this flags a
// row that a newer row on the same subject overtook, with no evidence
// keys at all.
//
//	NewIndex(docs, holders) ─► Index (holders: a search index's postings, or nil)
//	Index.Find(targets)
//	  ├─ each target's planned clauses: a sentence holding a planned
//	  │  marker (planned, not yet, TODO, will add, later …) and no done
//	  │  marker; a row with fact_state planned or proposed is one as a whole
//	  ├─ its subject: the clause's and the row's title words that are rare
//	  │  in docs (at most rareFloor rows, or 1 in rareShare of them)
//	  ├─ a NEWER doc (written after the target) with a done clause
//	  │  (shipped, released, implemented, is live …, not negated or
//	  │  future) holding enough of the subject (with its own title words),
//	  │  and no planned clause of its own on that subject
//	  └─ Finding{ID, By: the newest such doc} ─► Note: "stale: a newer row says it is done: <by> (<day>)"
//
// Precision before recall: a flag on a row that is still true sends an
// agent to check for nothing, so the subject must match on two or more
// rare words (one when the subject is a single identifier such as
// retry_queue), at least half of them up to four, and a row is never
// dropped, only marked. Pure: no I/O, no clock.
package staleclaim

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
)

// Doc is one row as the check reads it.
type Doc struct {
	ID          string
	Title, Body string
	// At is when the row was last written: a contradicting row must be
	// newer than the row it contradicts.
	At time.Time
	// State is the row's fact_state ("" when unset): planned or proposed
	// makes the row's whole text a planned claim, live, fixed, built or
	// shipped its title a done claim.
	State string
	// History marks a row read as history already (superseded, past its
	// valid_until): it is neither marked nor marks another.
	History bool
}

// Finding is one row a newer row contradicts.
type Finding struct {
	// ID is the older row, the one with the planned claim.
	ID string
	// By is the newest row stating the subject as done; ByAt its time.
	By   string
	ByAt time.Time
	// Words are the subject words both rows hold, sorted.
	Words []string
}

// Note is the mark search, ask and lint print on the older row: the
// newer row's id and day, so a reader opens it with lore show --id.
func (f Finding) Note() string {
	return "stale: a newer row says it is done: " + f.By + " (" + f.ByAt.UTC().Format(time.DateOnly) + ")"
}

// Holders is the place in docs of every doc that may hold word: a
// superset of the docs holding it (a search index's postings), nil when
// none does. It makes Find's cost follow the docs holding the subject
// words, not every doc's text.
type Holders func(word string) []int32

// Index is a set of docs prepared for Find, each text lowercased on first
// use. Not safe for concurrent use: one command builds and asks its own.
type Index struct {
	docs    []Doc
	lower   []string
	lowered []bool
	at      map[string]int
	holders Holders
	df      map[string]int
	// post is each word's docs (wordsOf), built on first use when
	// holders is nil: one pass over the texts instead of one per word.
	post map[string][]int32
}

// NewIndex prepares docs (kept, not copied) for Find; holders may be nil
// (each word is then looked for in every doc's text).
func NewIndex(docs []Doc, holders Holders) *Index {
	ix := &Index{docs: docs, lower: make([]string, len(docs)), lowered: make([]bool, len(docs)), at: make(map[string]int, len(docs)), holders: holders, df: map[string]int{}}
	for i, d := range docs {
		if _, dup := ix.at[d.ID]; !dup {
			ix.at[d.ID] = i
		}
	}
	return ix
}

// lowerOf is doc i's title and body lowercased, built once.
func (ix *Index) lowerOf(i int) string {
	if !ix.lowered[i] {
		ix.lower[i], ix.lowered[i] = strings.ToLower(ix.docs[i].Title+"\n"+ix.docs[i].Body), true
	}
	return ix.lower[i]
}

// holding is the docs that may hold w: holders' answer, else every doc
// whose text holds w.
func (ix *Index) holding(w string) []int32 {
	if ix.holders != nil {
		return ix.holders(w)
	}
	if ix.post == nil {
		ix.post = map[string][]int32{}
		for i := range ix.docs {
			for v := range wordsOf(ix.lowerOf(i)) {
				ix.post[v] = append(ix.post[v], int32(i))
			}
		}
	}
	return ix.post[w]
}

// Find is the finding of each doc named in ids that a newer doc of the
// index contradicts, in ids order; an id not in the index is skipped.
func (ix *Index) Find(ids []string) []Finding {
	var out []Finding
	for _, id := range ids {
		i, ok := ix.at[id]
		if !ok || ix.docs[i].History {
			continue
		}
		if f, ok := ix.check(i); ok {
			out = append(out, f)
		}
	}
	return out
}

// FindAll is Find over every doc of the index.
func (ix *Index) FindAll() []Finding {
	ids := make([]string, 0, len(ix.docs))
	for _, d := range ix.docs {
		ids = append(ids, d.ID)
	}
	return ix.Find(ids)
}

// rareFloor and rareShare say when a word is rare enough to name a
// subject: held by at most rareFloor docs, or by at most one doc in
// rareShare when the index is large.
const (
	rareFloor = 5
	rareShare = 25
	// maxNeed caps how many subject words a newer row must hold: a long
	// planned sentence names more than its subject.
	maxNeed = 4
)

// Markers, matched as whole phrases at word boundaries (phraseSet), never
// as regexps: Go's backtracking over dozens of alternatives on every
// sentence of every row cost ~80 ms of a warm ask on a store of long
// notes. planPhrases make a planned or not-yet claim, donePhrases a done
// claim; notDoneWords make a done marker in the same sentence no
// statement of fact (a negation, a condition, the future).
var (
	planPhrases = append([]string{"planned", "planning", "plan to", "plans to", "not yet", "not implemented", "unimplemented",
		"not supported", "not built", "not done", "not started", "todo", "to-do", "will add", "will ship", "will build",
		"will implement", "will support", "will land", "will release", "will introduce", "will move", "will switch",
		"will replace", "coming soon", "someday", "in the future", "future work", "for later", "pending", "backlog",
		"work in progress", "wip", "proposed", "upcoming", "next release", "next version", "on the roadmap"}, laterPhrases()...)
	donePhrases = []string{"shipped", "released", "landed", "implemented", "completed", "finished", "merged", "deployed",
		"launched", "rolled out", "went live", "is live", "now live", "is done", "are done", "now done", "is in place",
		"now exists", "exists now", "now supports", "now available", "available now", "fixed", "was added", "were added",
		"has been added", "have been added", "was built", "is built", "done:"}
	notDoneWords = []string{"not", "never", "no longer", "if", "unless", "once", "until", "when", "should", "would",
		"could", "might", "will", "won't", "yet to", "to be"}
	planSet, doneSet, notDoneSet = newPhraseSet(planPhrases), newPhraseSet(donePhrases), newPhraseSet(notDoneWords)
	plannedState                 = map[string]bool{"planned": true, "proposed": true}
	doneState                    = map[string]bool{"live": true, "fixed": true, "built": true, "shipped": true}
)

// laterPhrases are "do later", "ship it later", "fix this later" …: a verb
// of building, an optional it / this / that, then later.
func laterPhrases() []string {
	var out []string
	for _, v := range []string{"do", "ship", "add", "build", "implement", "fix", "handle"} {
		for _, o := range []string{"", "it ", "this ", "that "} {
			out = append(out, v+" "+o+"later")
		}
	}
	return out
}

// phraseSet is a list of phrases keyed by their first run of letters and
// digits ("to" for to-do and to be), so a text is matched in one pass over
// its words with a map lookup each, however many phrases there are (one
// strings.Index per phrase over a long note cost more than the rest of the
// check).
type phraseSet map[string][]string

// newPhraseSet indexes phrases by their first run of letters and digits.
func newPhraseSet(phrases []string) phraseSet {
	m := phraseSet{}
	for _, p := range phrases {
		j := 0
		for j < len(p) && isAlnum(p[j]) {
			j++
		}
		m[p[:j]] = append(m[p[:j]], p)
	}
	return m
}

// at is the byte offsets in lowercased s where a phrase of m starts as
// whole words (no letter or digit right before it, nor right after it
// when it ends in one), in order; the first only when first is set.
func (m phraseSet) at(s string, first bool) []int {
	var out []int
	for i := 0; i < len(s); {
		if !isAlnum(s[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && isAlnum(s[j]) {
			j++
		}
		if i == 0 || !isWordByte(s[i-1]) {
			for _, p := range m[s[i:j]] {
				e := i + len(p)
				if strings.HasPrefix(s[i:], p) && (!isWordByte(p[len(p)-1]) || e == len(s) || !isWordByte(s[e])) {
					out = append(out, i)
					if first {
						return out
					}
					break
				}
			}
		}
		i = j
	}
	return out
}

// holds reports whether lowercased s holds a phrase of m as whole words.
func (m phraseSet) holds(s string) bool { return len(m.at(s, true)) > 0 }

// notDone reports whether s holds a word that makes a done marker in it no
// statement of fact (a negation, a condition, the future, any n't).
func notDone(s string) bool {
	return strings.Contains(s, "n't") || notDoneSet.holds(s)
}

// clause is one sentence of a doc, lowercased, with its words.
type clause struct {
	text  string
	words map[string]bool
}

// parsed is a doc's planned and done clauses and its title words.
type parsed struct {
	title         map[string]bool
	planned, done []clause
}

// parse splits doc i into clauses and keeps the planned and the done ones.
func (ix *Index) parse(i int) parsed {
	d := ix.docs[i]
	p := parsed{title: wordsOf(strings.ToLower(d.Title))}
	for _, s := range clauses(ix.lowerOf(i)) {
		hasPlan, hasDone := planSet.holds(s), doneIn(s)
		switch {
		case hasPlan && !hasDone:
			p.planned = append(p.planned, clause{text: s, words: wordsOf(s)})
		case hasDone && !hasPlan && !notDone(s):
			p.done = append(p.done, clause{text: s, words: wordsOf(s)})
		}
	}
	state := strings.ToLower(strings.TrimSpace(d.State))
	if plannedState[state] {
		first, _, _ := strings.Cut(ix.lowerOf(i), "\n\n")
		p.planned = append(p.planned, clause{text: first, words: wordsOf(first)})
	}
	if doneState[state] {
		p.done = append(p.done, clause{text: strings.ToLower(d.Title), words: p.title})
	}
	return p
}

// doneIn reports whether s holds a done marker that a word right before
// it does not turn around ("not implemented", "to be shipped").
func doneIn(s string) bool {
	for _, k := range doneSet.at(s, false) {
		before := s[max(0, k-12):k]
		if !strings.Contains(before, "not ") && !strings.Contains(before, "un") && !strings.Contains(before, "be ") && !strings.Contains(before, "n't ") {
			return true
		}
	}
	return false
}

// check is the finding of doc i, if a newer doc contradicts it.
func (ix *Index) check(i int) (Finding, bool) {
	if !planSet.holds(ix.lowerOf(i)) && !plannedState[strings.ToLower(ix.docs[i].State)] {
		return Finding{}, false
	}
	me := ix.parse(i)
	var best Finding
	found := false
	for _, pc := range me.planned {
		subject := ix.rare(union(pc.words, me.title))
		need, ok := needOf(subject)
		if !ok {
			continue
		}
		// the row says it is done itself ("planned in May; shipped in June")
		if slices.ContainsFunc(me.done, func(c clause) bool { return overlap(union(c.words, me.title), subject) >= need }) {
			continue
		}
		for _, j := range ix.candidates(subject, need) {
			d := ix.docs[j]
			if j == i || d.History || d.ID == ix.docs[i].ID || !d.At.After(ix.docs[i].At) || (found && !d.At.After(best.ByAt)) {
				continue
			}
			if !holdsEnough(ix.lowerOf(j), subject, need) {
				continue
			}
			them := ix.parse(j)
			var hit []string
			for _, dc := range them.done {
				if w := shared(union(dc.words, them.title), subject); len(w) >= need && len(w) > len(hit) {
					hit = w
				}
			}
			if hit == nil {
				continue
			}
			// the newer row still plans the same thing ("shipped for
			// webhooks; for email still planned")
			if slices.ContainsFunc(them.planned, func(c clause) bool { return overlap(c.words, subject) >= need }) {
				continue
			}
			best, found = Finding{ID: ix.docs[i].ID, By: d.ID, ByAt: d.At, Words: hit}, true
		}
	}
	return best, found
}

// candidates is the docs that may hold at least need of the subject
// words, in doc order.
func (ix *Index) candidates(subject []string, need int) []int {
	count := map[int32]int{}
	for _, w := range subject {
		for _, d := range ix.holding(w) {
			count[d]++
		}
	}
	var out []int
	for d, n := range count {
		if n >= need && int(d) < len(ix.docs) {
			out = append(out, int(d))
		}
	}
	slices.Sort(out)
	return out
}

// isWordByte reports whether b is a letter or digit (ASCII; any other
// byte of a UTF-8 rune counts too, so a hint inside a word never starts one).
func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b >= 0x80
}

// rare is the words of ws held by few docs of the index, sorted.
func (ix *Index) rare(ws map[string]bool) []string {
	limit := max(rareFloor, len(ix.docs)/rareShare)
	var out []string
	for w := range ws {
		n, ok := ix.df[w]
		if !ok {
			n = len(ix.holding(w))
			ix.df[w] = n
		}
		if n <= limit {
			out = append(out, w)
		}
	}
	slices.Sort(out)
	return out
}

// needOf is how many subject words a newer row must hold, and whether the
// subject can be matched at all: two or more of a subject of two or more
// words, at least half up to maxNeed; one when the subject is a single
// identifier (retry_queue, v2.api), never a single plain word.
func needOf(subject []string) (int, bool) {
	switch n := len(subject); {
	case n == 0:
		return 0, false
	case n == 1:
		return 1, strings.ContainsAny(subject[0], "_.-0123456789")
	default:
		return max(2, min(maxNeed, (n+1)/2)), true
	}
}

// holdsEnough is a cheap test before a doc is split: its text holds at
// least need of the subject words (as substrings).
func holdsEnough(lower string, subject []string, need int) bool {
	n := 0
	for _, w := range subject {
		if strings.Contains(lower, w) {
			n++
			if n >= need {
				return true
			}
		}
	}
	return false
}

// clauses splits lowercased text into sentences: at a line break, a
// semicolon, or a full stop, question or exclamation mark before a space.
func clauses(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		end := c == '\n' || c == ';'
		if !end && (c == '.' || c == '!' || c == '?') {
			end = i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n'
		}
		if end {
			if t := strings.TrimSpace(s[start:i]); t != "" {
				out = append(out, t)
			}
			start = i + 1
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		out = append(out, t)
	}
	return out
}

// wordsOf is the subject words of lowercased s: three letters or more,
// no filler or marker word, a plural as its singular.
func wordsOf(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range tokens(s) {
		if len(w) < 3 || filler[w] || allDigits(w) {
			continue
		}
		out[singular(w)] = true
	}
	return out
}

// tokens is the words of lowercased s: runs of ASCII letters and digits,
// joined across a single _ . or - between two of them (retry_queue,
// v0.4, dead-letter stay one word).
func tokens(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if !isAlnum(s[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && (isAlnum(s[j]) || (s[j] == '_' || s[j] == '.' || s[j] == '-') && j+1 < len(s) && isAlnum(s[j+1])) {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

// isAlnum reports whether b is an ASCII lowercase letter or digit.
func isAlnum(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }

// singular is w without a plural s (queues → queue, entries → entry).
func singular(w string) string {
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	}
	return w
}

// allDigits reports whether w is a number (a year, a count): no subject.
func allDigits(w string) bool {
	return strings.IndexFunc(w, func(r rune) bool { return !unicode.IsDigit(r) && r != '.' }) < 0
}

// union is the words of a and b.
func union(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for w := range a {
		out[w] = true
	}
	for w := range b {
		out[w] = true
	}
	return out
}

// shared is the subject words ws holds, sorted.
func shared(ws map[string]bool, subject []string) []string {
	var out []string
	for _, w := range subject {
		if ws[w] {
			out = append(out, w)
		}
	}
	return out
}

// overlap is how many subject words ws holds.
func overlap(ws map[string]bool, subject []string) int {
	return len(shared(ws, subject))
}

// filler are words that name no subject: function words, the markers
// themselves and the words notes use for any change.
var filler = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`the and for with from that this these those into onto then than there their they them its it's
		are was were been being has have had having will would should could can may might must shall our ours you your
		not yet now still also just only very more most less some any all each every both either neither other such same
		what when where which who whom whose why how about after before again over under out off once here very much many
		planned planning plan plans todo to-do later soon someday future pending backlog progress wip proposed upcoming
		next release version roadmap coming work implemented unimplemented implement supported support built build done
		started start shipped ship released landed land completed complete finished finish merged deployed deploy
		launched launch rolled went live place exists exist available fixed fix added add was is be
		note notes status update day week month today yesterday tomorrow new old first last one two three`) {
		m[w] = true
	}
	return m
}()

// StateOf is a stored row's fact_state ("" when unset), the Doc.State
// lint and search read from the row itself.
func StateOf(r row.Row) string {
	for _, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil || en.Name != meta.KeyFactState {
			continue
		}
		var s string
		if json.Unmarshal(en.V, &s) == nil {
			return s
		}
	}
	return ""
}
