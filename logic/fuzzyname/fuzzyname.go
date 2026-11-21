// Package fuzzyname finds the names a mistyped word means: a trigram
// index over names (resource names and their aliases, devco D-817), each
// candidate it yields checked by optimal string alignment distance.
//
//	Add(term, owner) ─▶ trigrams of "  term " ─▶ postings term ids
//	Near(word, budget)
//	  ─▶ terms sharing a trigram with word (the index: no full scan)
//	  ─▶ OSA(word, term) ≤ budget (a swap of two neighbours is one edit)
//	  ─▶ the closest terms only: alise finds alice, not alice and alix
//
// The index is pure and in memory: callers build it from the rows they
// already read (lore ask's one lens read, a resolver's row list), so a
// lookup costs no extra I/O.
package fuzzyname

import (
	"slices"
	"strings"
)

// Hit is one indexed term near a word.
type Hit struct {
	// Owner is what Add was given with the term (a row index).
	Owner int
	// Term is the indexed text (lower case).
	Term string
	// Dist is its OSA distance from the word.
	Dist int
}

// Index is a trigram index of terms. The zero value is not usable; call
// New.
type Index struct {
	terms  []string
	owners []int
	grams  map[string][]int
}

// New is an empty index.
func New() *Index { return &Index{grams: map[string][]int{}} }

// Add indexes term (case is ignored) for owner. An empty term is skipped.
func (x *Index) Add(term string, owner int) {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return
	}
	id := len(x.terms)
	x.terms = append(x.terms, term)
	x.owners = append(x.owners, owner)
	for _, g := range trigrams(term) {
		if p := x.grams[g]; len(p) == 0 || p[len(p)-1] != id {
			x.grams[g] = append(p, id)
		}
	}
}

// Len is how many terms are indexed.
func (x *Index) Len() int { return len(x.terms) }

// Near is every term within budget edits of word, closest first; only
// the terms at the smallest distance found are returned (two equally
// close terms both are). Exact matches (distance 0) are returned too.
func (x *Index) Near(word string, budget int) []Hit {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" || budget < 0 {
		return nil
	}
	seen := map[int]bool{}
	var out []Hit
	best := budget + 1
	for _, g := range trigrams(word) {
		for _, id := range x.grams[g] {
			if seen[id] {
				continue
			}
			seen[id] = true
			d := OSA(word, x.terms[id], budget)
			if d > budget || d > best {
				continue
			}
			if d < best {
				out, best = out[:0], d
			}
			out = append(out, Hit{Owner: x.owners[id], Term: x.terms[id], Dist: d})
		}
	}
	slices.SortStableFunc(out, func(a, b Hit) int { return strings.Compare(a.Term, b.Term) })
	return out
}

// trigrams are the three-letter windows of "  s " (two leading spaces and
// one trailing, so a word's first letters weigh in and a short word still
// has some), each once.
func trigrams(s string) []string {
	r := []rune("  " + s + " ")
	out := make([]string, 0, len(r))
	for i := 0; i+3 <= len(r); i++ {
		g := string(r[i : i+3])
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// OSA is the optimal string alignment distance of a and b: insertions,
// deletions, substitutions and swaps of two neighbouring letters (lumne →
// lumen is one). Words whose lengths differ by more than limit return
// limit+1 without the table walk.
func OSA(a, b string, limit int) int {
	x, y := []rune(a), []rune(b)
	if d := len(x) - len(y); d > limit || d < -limit {
		return limit + 1
	}
	prev2 := make([]int, len(y)+1)
	prev := make([]int, len(y)+1)
	cur := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && x[i-1] == y[j-2] && x[i-2] == y[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(y)]
}

// Budget is how many letters a word may be off a name: none under four
// letters (too many short words are a letter off each other), one up to
// seven, two from eight on.
func Budget(word string) int {
	switch n := len([]rune(word)); {
	case n < 4:
		return 0
	case n < 8:
		return 1
	}
	return 2
}
