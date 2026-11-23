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
// thousands. Titles are a map lookup; bodies use prefix filtering (two
// word sets with Jaccard >= t share a word among the first
// |x| - ceil(t*|x|) + 1 words of each, ordered rarest first), so only
// rows sharing a rare word are compared in full.
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

// wordSet is the distinct words of s, sorted.
func wordSet(s string) []string {
	w := words(s)
	slices.Sort(w)
	return slices.Compact(w)
}

// prefixLen is how many of the first words of a set of n words two sets
// meeting the threshold must share one of.
func prefixLen(n int) int {
	return n - int(math.Ceil(Threshold*float64(n))) + 1
}

// jaccard is |a∩b| / |a∪b| of two sorted word sets.
func jaccard(a, b []string) float64 {
	i, j, both := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch c := strings.Compare(a[i], b[j]); {
		case c == 0:
			both++
			i++
			j++
		case c < 0:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - both
	if union == 0 {
		return 0
	}
	return float64(both) / float64(union)
}

// Find pairs each incoming row with the existing row of the same table it
// near-duplicates (see the package comment), in the order of incoming.
func Find(incoming, existing []Item) []Match {
	if len(incoming) == 0 || len(existing) == 0 {
		return nil
	}
	type key struct{ table, title string }
	byTitle := map[key]int{}
	for i, e := range existing {
		if t := NormTitle(e.Title); t != "" {
			if _, ok := byTitle[key{e.Table, t}]; !ok {
				byTitle[key{e.Table, t}] = i
			}
		}
	}
	// word sets, and how many bodies hold each word (rarest first order)
	freq := map[string]int{}
	sets := func(items []Item) [][]string {
		out := make([][]string, len(items))
		for i, it := range items {
			out[i] = wordSet(it.Body)
			for _, w := range out[i] {
				freq[w]++
			}
		}
		return out
	}
	inSets, exSets := sets(incoming), sets(existing)
	rarest := func(ws []string) []string {
		o := slices.Clone(ws)
		slices.SortFunc(o, func(a, b string) int {
			return cmp.Or(cmp.Compare(freq[a], freq[b]), strings.Compare(a, b))
		})
		return o
	}
	type post struct{ table, word string }
	index := map[post][]int{}
	for i, ws := range exSets {
		if len(ws) < MinWords {
			continue
		}
		r := rarest(ws)
		for _, w := range r[:prefixLen(len(r))] {
			p := post{existing[i].Table, w}
			index[p] = append(index[p], i)
		}
	}
	var out []Match
	for i, in := range incoming {
		if t := NormTitle(in.Title); t != "" {
			if j, ok := byTitle[key{in.Table, t}]; ok && existing[j].ID != in.ID {
				out = append(out, Match{New: in, Existing: existing[j], Why: SameTitle, Percent: 100})
				continue
			}
		}
		ws := inSets[i]
		if len(ws) < MinWords {
			continue
		}
		r := rarest(ws)
		best, bestScore := -1, 0.0
		seen := map[int]bool{}
		for _, w := range r[:prefixLen(len(r))] {
			for _, j := range index[post{in.Table, w}] {
				if seen[j] || existing[j].ID == in.ID {
					continue
				}
				seen[j] = true
				if s := jaccard(ws, exSets[j]); s >= Threshold && (s > bestScore || s == bestScore && j < best) {
					best, bestScore = j, s
				}
			}
		}
		if best >= 0 {
			out = append(out, Match{New: in, Existing: existing[best], Why: SimilarBody, Percent: int(math.Floor(bestScore * 100))})
		}
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
	at := make(map[string]int, len(items))
	for i, it := range items {
		at[it.ID] = i
	}
	type pair struct{ old, new int }
	seen := map[pair]bool{}
	var out []Match
	add := func(m Match) {
		a, b := at[m.Existing.ID], at[m.New.ID]
		if a > b {
			a, b = b, a
		}
		if a == b || seen[pair{a, b}] {
			return
		}
		seen[pair{a, b}] = true
		m.Existing, m.New = items[a], items[b]
		out = append(out, m)
	}
	for _, it := range items {
		if ref := strings.TrimSpace(it.SourceRef); ref != "" && ref != it.ID {
			if j, ok := at[ref]; ok {
				add(Match{New: it, Existing: items[j], Why: RewriteOf, Percent: 100})
			}
		}
	}
	for _, m := range Find(items, items) {
		add(m)
	}
	slices.SortStableFunc(out, func(x, y Match) int { return cmp.Compare(at[x.New.ID], at[y.New.ID]) })
	return out
}
