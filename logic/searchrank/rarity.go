package searchrank

// rarity.go — how much a held word weighs when the row holding it is
// weaker evidence than a knowledge row (devco:D-1007).
//
// Why: Score counts every held word as one, which is right between two
// knowledge rows, but two kinds of hit took first places on words that
// say nothing about the question:
//
//   - a card (a team, a person, a resource) holding a common word in its
//     description: a team "@ops-admins — Ops admins (company store owners)"
//     ranked first for a question about an admin page, on "admin",
//     "store" and "company";
//   - a stale fact (its evidence changed since it was verified) holding
//     more of the question's common words than a fresh row that holds
//     its rare one.
//
// For those two, a held word counts by its rarity (Share): the query's
// rarest word counts 1, a word held by many more rows far less. A card's
// own name and handle are what it is called, so a word there counts 1:
// `lore search ops-admins` and "who is alice ng" still find the card first.
//
//	per-word hits (df) ─▶ Share: ln(1+M/df) / ln(1+M/min df)   (M = most hits of a word)
//	held words ─▶ Weight: average over the held words of 1 (fresh row,
//	              card name) or Share (stale row, card description)
//	matched·Weight ─▶ the score the merged list is ordered by
//
// The printed score stays the match's own (Score); only the order uses
// the weighted one, as a stale fact's 0.9 already did.

import "math"

// Share is each word's rarity among the query's words, in (0, 1]: 1 for
// the word fewest rows hold, less for a word more rows hold (an inverse
// document frequency, ln(1+M/df), over the rarest word's). df[k] is how
// many rows hold word k (0 counts as 1: a word nothing holds is as rare
// as can be). Every word is 1 when all are held equally often, and
// without words Share is empty.
func Share(df []int) []float64 {
	if len(df) == 0 {
		return nil
	}
	most := 1
	for _, d := range df {
		most = max(most, d)
	}
	idf := make([]float64, len(df))
	top := 0.0
	for k, d := range df {
		idf[k] = math.Log1p(float64(most) / float64(max(d, 1)))
		top = max(top, idf[k])
	}
	out := make([]float64, len(df))
	for k := range idf {
		out[k] = idf[k] / top
	}
	return out
}

// Weight is the share of its matched count a hit ranks with: the
// average, over the words it holds (held, by Terms index), of 1 for a
// word that counts in full (full[k]: a fresh row's word, a card's name
// or handle word) and share[k] for one that counts by rarity. 1 when it
// holds no word the slices know of, or when they do not line up with
// one another (a condition search: every hit matched all there was).
func Weight(held, full []bool, share []float64) float64 {
	if len(held) == 0 || len(held) != len(share) || len(full) != len(held) {
		return 1
	}
	sum, n := 0.0, 0
	for k, h := range held {
		if !h {
			continue
		}
		n++
		if full[k] {
			sum++
		} else {
			sum += share[k]
		}
	}
	if n == 0 {
		return 1
	}
	return sum / float64(n)
}

// Weighted is Score with a weight on the matched words (Weight): a hit
// scored s on n words, holding matched of them, ranks with the matched
// part scaled by weight and its bonus (names, title share, rank) kept.
// weight 1 gives s back.
func Weighted(s float64, matched, n int, weight float64) float64 {
	if weight >= 1 || n == 0 {
		return s
	}
	return s - float64(matched)*(1-weight)/float64(n+1)
}
