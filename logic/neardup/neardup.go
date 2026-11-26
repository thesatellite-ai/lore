// Package neardup finds rows that say the same thing under different ids
// (devco D-724).
//
// Why it exists: moving a repo's notes into a store folder that already
// holds curated rows (store adopt, a bulk move) keeps every id, so a rule
// someone had already written into the folder by hand and the repo's own
// copy of it both survive: two "use portless for dev URLs" rules in one
// folder, both rendered into every agent's context. Merging them is a
// judgement call (which wording, which status, which relations), so lore
// never merges; it lists each pair after the move with the commands to
// compare them and trash one.
//
// What counts as a near-duplicate, per table (a rule never pairs with a
// decision):
//
//	same title     ─▶ the titles are equal once case, punctuation and
//	                  spacing are ignored ("Use  Portless!" = "use portless")
//	similar body   ─▶ otherwise, the bodies share at least Threshold of
//	                  their words (Jaccard over the word sets), each body
//	                  at least MinWords words long (two one-line bodies
//	                  "see the wiki" say nothing about each other)
//
// Each incoming row is paired with at most one existing row: the first
// same-title row, else the most similar body. Rows with the same id are
// one row (an update, not a duplicate) and never pair.
//
// Speed: a bulk move can bring thousands of rows into a folder of
// thousands, and lore doctor checks a whole store (170k notes on a
// 1M-commit history). Titles and bodies with the same words are a map
// lookup; other bodies use prefix filtering (two word sets with Jaccard
// >= t share a word among the first |x| - ceil(t*|x|) + 1 words of each,
// ordered rarest first), so only rows sharing a rare word are compared in
// full. A word held by more bodies than postingCap brings no candidates
// (devco:D-1016): a small shared vocabulary made every note a candidate
// of every other, so the check grew with the square of the notes.
//
// Layer L1: pure, no I/O, stdlib only.
package neardup

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"unicode"
)

// Threshold is the share of words two bodies must have in common (Jaccard
// over their word sets) to count as near-duplicates.
const Threshold = 0.8

// MinWords is the fewest distinct words a body needs before its
// similarity to another body means anything.
const MinWords = 4

// Reason says why two rows were paired.
type Reason string

const (
	// SameTitle: the normalised titles are equal.
	SameTitle Reason = "same title"
	// SimilarBody: the bodies share at least Threshold of their words.
	SimilarBody Reason = "similar body"
	// RewriteOf: the newer row's source_ref names the older row's id and
	// both are live (devco D-926: a rewrite that left the original live).
	RewriteOf Reason = "rewrite of"
)

// Item is one row as the detector sees it: its table and id, and the
// text people read (Title is its Headline, "" when it has none).
type Item struct {
	Table string
	ID    string
	Title string
	Body  string
	// SourceRef is the row's provenance pointer (source_ref); when it
	// names another live row's id the two are a RewriteOf pair (Within).
	SourceRef string
}

// Match pairs an incoming row with the existing row it repeats. Percent
// is the body similarity (0-100) for SimilarBody, 100 for SameTitle.
type Match struct {
	New      Item
	Existing Item
	Why      Reason
	Percent  int
	at       [2]int // Within: the positions of Existing and New in the set
}

// MinHeadlineWords is the fewest words a body's first line needs to stand
// in for a title (a "## Notes" heading names nothing).
const MinHeadlineWords = 3

// MinSummaryWords is the fewest words a summary needs to name its row
// (devco D-926: two rules summed up as "ok" are not one rule; "use
// portless" names one).
const MinSummaryWords = 2

// Headline is what a row is called: its title, else its one-line summary
// when it has MinSummaryWords words, else the first line of its body when
// that line has MinHeadlineWords words (a rule has no title: "Use
// portless for dev URLs." is its name).
func Headline(title, summary, body string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	if len(words(summary)) >= MinSummaryWords {
		return summary
	}
	first, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	if len(words(first)) < MinHeadlineWords {
		return ""
	}
	return first
}

// NormTitle is a title as the detector compares it: lower case, every run
// of characters that are not letters or digits one space, trimmed.
func NormTitle(s string) string {
	return strings.Join(words(s), " ")
}

// words splits s into lower-case words of letters and digits.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// Vocab numbers the words of the bodies it has seen, so a body is kept as
// a short sorted list of numbers instead of its text (devco:D-1016: lore
// doctor held every scanned row decoded for the whole check, 111 MB live
// at 20k notes; a Doc holds no body).
type Vocab struct{ ids map[string]uint32 }

// NewVocab is an empty Vocab; Docs compared with each other must come
// from one Vocab.
func NewVocab() *Vocab { return &Vocab{ids: map[string]uint32{}} }

// Doc is one row prepared for the detector: its Item without the Body
// (Item.Body is ""), the body kept as its distinct word numbers.
type Doc struct {
	Item
	title string   // NormTitle(Item.Title)
	words []uint32 // the body's distinct words (Vocab numbers), sorted
}

// Doc prepares it: the normalised title and the body's word numbers; the
// body text is not kept, so a caller can stream rows into Docs and let
// each row go.
func (v *Vocab) Doc(it Item) Doc {
	ws := words(it.Body)
	set := make([]uint32, 0, len(ws))
	for _, w := range ws {
		id, ok := v.ids[w]
		if !ok {
			id = uint32(len(v.ids))
			v.ids[strings.Clone(w)] = id // w is a slice of the lowered body: keep only the word
		}
		set = append(set, id)
	}
	slices.Sort(set)
	it.Body = ""
	return Doc{Item: it, title: NormTitle(it.Title), words: slices.Clip(slices.Compact(set))}
}

// MinPostings is the document-frequency cap of a small set: up to this
// many bodies, every word can bring a candidate (so a folder of a few
// dozen notes is compared as before the cap).
const MinPostings = 50

// MaxPostings bounds the cap at any size, so the comparisons per body
// stay bounded and the check stays near linear in the number of bodies.
const MaxPostings = 200

// postingCap is how many bodies may hold a word before it stops bringing
// candidates in a bounded join (WithinStore, devco:D-1016): 1% of the
// bodies, between MinPostings and MaxPostings. A word in thousands of
// bodies says nothing about two of them being copies, and pairing through
// it is what made a whole store quadratic (every note of a small
// vocabulary compared with every other). Two copies still pair through
// the rarer words they share, and bodies with the same words through an
// exact-set lookup.
func postingCap(bodies int) int {
	return min(max(MinPostings, bodies/100), MaxPostings)
}

// MaxCandidates is how many postings one body may visit in a bounded join
// (rarest words first, so a copy sharing a rare word is met at once): it
// bounds the work per body whatever the store's vocabulary (devco:D-1016).
const MaxCandidates = MaxPostings

// prefixLen is how many of the first words of a set of n words two sets
// meeting the threshold must share one of.
func prefixLen(n int) int {
	return n - int(math.Ceil(Threshold*float64(n))) + 1
}

// jaccard is |a∩b| / |a∪b| of two sorted word sets, 0 when it is under
// Threshold (it stops once the words left cannot bring it there: most
// candidates of a big store share a word or two and fail early).
func jaccard(a, b []uint32) float64 {
	// |a∩b| / (|a|+|b|-|a∩b|) >= t  ⇔  |a∩b| >= t(|a|+|b|)/(1+t)
	need := int(math.Ceil(Threshold * float64(len(a)+len(b)) / (1 + Threshold)))
	i, j, both := 0, 0, 0
	for i < len(a) && j < len(b) {
		if both+min(len(a)-i, len(b)-j) < need {
			return 0
		}
		switch {
		case a[i] == b[j]:
			both++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - both
	if union == 0 || both < need {
		return 0
	}
	return float64(both) / float64(union)
}

// hit is one pairing find made: incoming[in] repeats existing[ex].
type hit struct {
	in, ex  int
	why     Reason
	percent int
}

// setKey is a body's table and word set as a map key (the exact-set
// lookup: bodies with the same distinct words pair without a posting).
func setKey(table string, ws []uint32) string {
	var b strings.Builder
	b.Grow(len(table) + 1 + 4*len(ws))
	b.WriteString(table)
	b.WriteByte(0)
	for _, w := range ws {
		b.WriteByte(byte(w))
		b.WriteByte(byte(w >> 8))
		b.WriteByte(byte(w >> 16))
		b.WriteByte(byte(w >> 24))
	}
	return b.String()
}

// titleKey is a normalised title within its table.
type titleKey struct{ table, title string }

// post is one posting list's key: a word within a table.
type post struct {
	table string
	word  uint32
}

// matcher is a set of existing docs prepared for pairing (titles, word
// frequencies, postings, exact word sets), so many incoming docs can be
// matched without preparing the set again (Index).
type matcher struct {
	existing []Doc
	byTitle  map[titleKey]int
	df       []int32 // how many bodies hold each word (the rarest-first order and the cap)
	capAt    int32
	index    map[post][]int32
	exact    map[string][]int32 // the first two existing docs of each word set
	sizes    []int32            // word counts, read without touching each Doc
	stamp    []int32            // stamp[j] == round: existing[j] already compared with this incoming doc
	round    int32
	budget   int // postings one incoming doc may visit (MaxCandidates when bounded)
}

// newMatcher prepares existing; the word frequencies count existing and
// also (the incoming docs of a one-off Find; nil for Within and Index).
// bounded applies postingCap and MaxCandidates (WithinStore: a whole
// store's check); otherwise every shared prefix word brings candidates
// and the answer is exact, as render, store adopt and docs import need.
func newMatcher(existing, also []Doc, bounded bool) *matcher {
	m := &matcher{existing: existing, byTitle: map[titleKey]int{}, index: map[post][]int32{}, exact: map[string][]int32{},
		sizes: make([]int32, len(existing)), stamp: make([]int32, len(existing)), capAt: math.MaxInt32, budget: math.MaxInt}
	for i, e := range existing {
		if e.title != "" {
			if _, ok := m.byTitle[titleKey{e.Table, e.title}]; !ok {
				m.byTitle[titleKey{e.Table, e.title}] = i
			}
		}
		m.sizes[i] = int32(len(e.words))
	}
	count := func(docs []Doc) {
		for _, d := range docs {
			for _, w := range d.words {
				if int(w) >= len(m.df) {
					m.df = append(m.df, make([]int32, int(w)+1-len(m.df))...)
				}
				m.df[w]++
			}
		}
	}
	count(existing)
	count(also)
	if bounded {
		m.capAt, m.budget = int32(postingCap(len(existing)+len(also))), MaxCandidates
	}
	for i, e := range existing {
		if len(e.words) < MinWords {
			continue
		}
		k := setKey(e.Table, e.words)
		if len(m.exact[k]) < 2 {
			m.exact[k] = append(m.exact[k], int32(i))
		}
		for _, w := range m.rarest(e.words) {
			if m.freq(w) <= m.capAt {
				p := post{e.Table, w}
				m.index[p] = append(m.index[p], int32(i))
			}
		}
	}
	return m
}

// freq is how many prepared bodies hold w (0 for a word only an incoming
// doc has).
func (m *matcher) freq(w uint32) int32 {
	if int(w) < len(m.df) {
		return m.df[w]
	}
	return 0
}

// rarest is the prefix of ws (prefixLen words) in rarest-first order.
func (m *matcher) rarest(ws []uint32) []uint32 {
	o := slices.Clone(ws)
	slices.SortFunc(o, func(a, b uint32) int { return cmp.Or(cmp.Compare(m.freq(a), m.freq(b)), cmp.Compare(a, b)) })
	return o[:prefixLen(len(o))]
}

// match pairs each incoming doc with the existing doc of its table it
// near-duplicates (see the package comment), in the order of incoming.
func (m *matcher) match(incoming []Doc) []hit {
	var out []hit
	existing := m.existing
	for i, in := range incoming {
		if in.title != "" {
			if j, ok := m.byTitle[titleKey{in.Table, in.title}]; ok && existing[j].ID != in.ID {
				out = append(out, hit{in: i, ex: j, why: SameTitle, percent: 100})
				continue
			}
		}
		ws := in.words
		if len(ws) < MinWords {
			continue
		}
		if c := m.exact[setKey(in.Table, ws)]; len(c) > 0 {
			if j := slices.IndexFunc(c, func(j int32) bool { return existing[j].ID != in.ID }); j >= 0 {
				out = append(out, hit{in: i, ex: int(c[j]), why: SimilarBody, percent: 100})
				continue
			}
		}
		// a set of n words meets the threshold with one of m words only
		// when Threshold*n <= m <= n/Threshold
		lo, hi := int(math.Ceil(Threshold*float64(len(ws)))), int(math.Floor(float64(len(ws))/Threshold))
		best, bestScore, budget := int32(-1), 0.0, m.budget
		m.round++
		for _, w := range m.rarest(ws) {
			if m.freq(w) > m.capAt || budget <= 0 {
				continue
			}
			for _, j := range m.index[post{in.Table, w}] {
				if budget--; budget < 0 {
					break
				}
				if m.stamp[j] == m.round {
					continue
				}
				m.stamp[j] = m.round
				if n := int(m.sizes[j]); n < lo || n > hi || existing[j].ID == in.ID {
					continue
				}
				if s := jaccard(ws, existing[j].words); s >= Threshold && (s > bestScore || s == bestScore && j < best) {
					best, bestScore = j, s
				}
			}
		}
		if best >= 0 {
			out = append(out, hit{in: i, ex: int(best), why: SimilarBody, percent: int(math.Floor(bestScore * 100))})
		}
	}
	return out
}

// find pairs each incoming doc with the existing doc of its table it
// near-duplicates; same says incoming and existing are one set (Within),
// so each body counts once; bounded is newMatcher's.
func find(incoming, existing []Doc, same, bounded bool) []hit {
	if len(incoming) == 0 || len(existing) == 0 {
		return nil
	}
	var also []Doc
	if !same {
		also = incoming
	}
	return newMatcher(existing, also, bounded).match(incoming)
}

// Index is a set of existing rows prepared once, for many Find calls
// (devco:D-1016: lore docs import compared each drafted section and
// paragraph with every stored note, preparing every note again each
// time). Its word frequencies are the existing rows' own; not safe for
// concurrent use.
type Index struct {
	items []Item
	v     *Vocab
	m     *matcher
}

// NewIndex prepares existing for Find.
func NewIndex(existing []Item) *Index {
	v := NewVocab()
	return &Index{items: existing, v: v, m: newMatcher(v.docs(existing), nil, false)}
}

// Find is the package Find of incoming against the indexed rows.
func (x *Index) Find(incoming []Item) []Match {
	if len(incoming) == 0 || len(x.items) == 0 {
		return nil
	}
	hits := x.m.match(x.v.docs(incoming))
	out := make([]Match, len(hits))
	for i, h := range hits {
		out[i] = Match{New: incoming[h.in], Existing: x.items[h.ex], Why: h.why, Percent: h.percent}
	}
	return out
}

// docs prepares items with one Vocab.
func (v *Vocab) docs(items []Item) []Doc {
	out := make([]Doc, len(items))
	for i, it := range items {
		out[i] = v.Doc(it)
	}
	return out
}

// Find pairs each incoming row with the existing row of the same table it
// near-duplicates (see the package comment), in the order of incoming.
func Find(incoming, existing []Item) []Match {
	v := NewVocab()
	hits := find(v.docs(incoming), v.docs(existing), false, false)
	out := make([]Match, len(hits))
	for i, h := range hits {
		out[i] = Match{New: incoming[h.in], Existing: existing[h.ex], Why: h.why, Percent: h.percent}
	}
	return out
}

// Within pairs the rows of one set that repeat each other (devco D-926:
// an import and its rewrite a month later, both live, so every pinned
// rule rendered twice). items must be in age order, oldest first: in each
// Match, Existing is the older row (the one to archive) and New the
// newer. A row whose SourceRef names another row of the set pairs with it
// first (RewriteOf, any table); otherwise the rules of Find apply. Each
// unordered pair is listed once, in the order of its newer row.
func Within(items []Item) []Match {
	out := WithinDocs(NewVocab().docs(items))
	for i := range out { // the caller's items, Body included
		out[i].Existing, out[i].New = items[out[i].at[0]], items[out[i].at[1]]
	}
	return out
}

// WithinDocs is Within over prepared docs (one Vocab, age order): a
// caller that streams rows keeps only their Docs (devco:D-1016). The
// Items of each Match have no Body.
func WithinDocs(docs []Doc) []Match { return within(docs, false) }

// WithinStore is WithinDocs for a whole store's rows (lore doctor's and
// lore lint's near_duplicate, devco:D-1016): candidates come only from
// words held by at most postingCap bodies and each body visits at most
// MaxCandidates postings, so the work grows near linearly with the rows.
// Bodies with the same words, titles and source_ref still always pair;
// two bodies that share only common words may not.
func WithinStore(docs []Doc) []Match { return within(docs, true) }

// within is WithinDocs (bounded: WithinStore).
func within(docs []Doc, bounded bool) []Match {
	at := make(map[string]int, len(docs))
	for i, d := range docs {
		at[d.ID] = i
	}
	type pair struct{ old, new int }
	seen := map[pair]bool{}
	var out []Match
	add := func(a, b int, why Reason, percent int) {
		if a > b {
			a, b = b, a
		}
		if a == b || seen[pair{a, b}] {
			return
		}
		seen[pair{a, b}] = true
		out = append(out, Match{Existing: docs[a].Item, New: docs[b].Item, Why: why, Percent: percent, at: [2]int{a, b}})
	}
	for i, d := range docs {
		if ref := strings.TrimSpace(d.SourceRef); ref != "" && ref != d.ID {
			if j, ok := at[ref]; ok {
				add(j, i, RewriteOf, 100)
			}
		}
	}
	for _, h := range find(docs, docs, true, bounded) {
		add(at[docs[h.ex].ID], at[docs[h.in].ID], h.why, h.percent)
	}
	slices.SortStableFunc(out, func(x, y Match) int { return cmp.Compare(x.at[1], y.at[1]) })
	return out
}
