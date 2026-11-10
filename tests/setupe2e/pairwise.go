//go:build setupe2e

package setupe2e

// pairwise.go — a deterministic pairwise covering subset of AllShapes:
// every pair of values of two different dimensions that some valid
// shape holds appears in at least one chosen shape. Greedy: repeatedly
// take the first shape (AllShapes order) covering the most uncovered
// pairs. No randomness, so the subset only changes when the dimensions do.

import "fmt"

// dimValues is a shape's value per dimension, as text, in Shape field order.
func dimValues(s Shape) [8]string {
	return [8]string{fmt.Sprint(s.GHAccounts), s.GHActive, s.SSH, s.Keys, fmt.Sprint(s.IncludeIf), s.LoreHome, s.Start, s.PushMode()}
}

// pairKey names one value pair of dimensions i < j.
type pairKey struct {
	i, j int
	a, b string
}

// shapePairs lists the 28 pairs one shape covers.
func shapePairs(s Shape) []pairKey {
	v := dimValues(s)
	out := make([]pairKey, 0, 28)
	for i := 0; i < len(v); i++ {
		for j := i + 1; j < len(v); j++ {
			out = append(out, pairKey{i, j, v[i], v[j]})
		}
	}
	return out
}

// PairwiseShapes is the covering subset.
func PairwiseShapes() []Shape {
	all := AllShapes()
	uncovered := map[pairKey]bool{}
	for _, s := range all {
		for _, p := range shapePairs(s) {
			uncovered[p] = true
		}
	}
	var out []Shape
	for len(uncovered) > 0 {
		best, bestN := -1, 0
		for i, s := range all {
			n := 0
			for _, p := range shapePairs(s) {
				if uncovered[p] {
					n++
				}
			}
			if n > bestN {
				best, bestN = i, n
			}
		}
		out = append(out, all[best])
		for _, p := range shapePairs(all[best]) {
			delete(uncovered, p)
		}
	}
	return out
}
