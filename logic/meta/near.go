package meta

import (
	"slices"
	"strings"
)

// Near-duplicate key names (METADATA 3.5 "Near-duplicates warn", 7.x
// "prints the three nearest existing keys"):
//
//	Nearest(name, existing, n)
//	  ├ same stem after dropping _ and a common suffix (_id, _no, _num,
//	  │ _ids, s, es) → distance 0
//	  ├ edit distance ≤ nearMaxDist (≤ 1 for names of nearShortLen or less)
//	  └ the n closest, ties by name
//
// P2-META-7: only names that are actually close are printed; a new key
// with no near name prints nothing extra.

// nearMaxDist is the largest edit distance that still counts as close.
const nearMaxDist = 2

// nearShortLen: names this short count as close at distance 1 only, so
// "env" and "dns" do not suggest each other.
const nearShortLen = 4

// nearSuffixes are the endings a stem drops (longest first).
var nearSuffixes = []string{"_ids", "_num", "_id", "_no", "es", "s"}

// stem is name lowercased with its common suffix and every '_' removed.
func stem(name string) string {
	s := strings.ToLower(name)
	for _, suf := range nearSuffixes {
		if t, ok := strings.CutSuffix(s, suf); ok && len(t) >= 2 {
			s = t
			break
		}
	}
	return strings.ReplaceAll(s, "_", "")
}

// Nearest returns up to n names of existing close to name (name itself
// excluded), closest first.
func Nearest(name string, existing []string, n int) []string {
	type cand struct {
		name string
		d    int
	}
	var out []cand
	ns := stem(name)
	for _, e := range existing {
		if e == name {
			continue
		}
		d := editDistance(strings.ToLower(name), strings.ToLower(e))
		if stem(e) == ns {
			d = 0
		}
		limit := nearMaxDist
		if len(name) <= nearShortLen || len(e) <= nearShortLen {
			limit = 1
		}
		if d <= limit {
			out = append(out, cand{e, d})
		}
	}
	slices.SortFunc(out, func(a, b cand) int {
		if a.d != b.d {
			return a.d - b.d
		}
		return strings.Compare(a.name, b.name)
	})
	names := make([]string, 0, min(n, len(out)))
	for i := 0; i < len(out) && i < n; i++ {
		if !slices.Contains(names, out[i].name) {
			names = append(names, out[i].name)
		}
	}
	return names
}

// editDistance is the Levenshtein distance of a and b (bytes; key names
// are snake_case ASCII).
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
