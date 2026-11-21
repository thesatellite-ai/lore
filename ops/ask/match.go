package ask

import (
	"slices"
	"strings"
)

// How a resource was found from the question (Resource.Match).
const (
	MatchName  = "name"  // the question names it: alice-ng, or one part of the name of 4+ letters (alice)
	MatchTypo  = "typo"  // a word one or two letters off its name: alise-ng, lumne
	MatchField = "field" // a word is one of its field values: a nickname (Bobby), a role (CFO)
)

// resourceRow is one resource as the lens holds it.
type resourceRow struct {
	ID, Place, Kind, Name, Updated string
	// Fields in the kind's order of the stored object (name → value).
	Fields []fieldValue
	// History is the field history, oldest first.
	History []historyEntry
}

// fieldValue is one field of a resource.
type fieldValue struct{ Name, Value string }

// historyEntry is one change of one field (resources.history).
type historyEntry struct {
	Field, From, To, At string
}

// match is one resource the question is about and how it was found.
type match struct {
	res *resourceRow
	how string
	// field names the field whose value matched (MatchField).
	field string
}

// matchResources finds the resources a question is about: by name first
// (a token is the name, or one part of it), then by a name one or two
// letters off (a typo), then by a field value (a nickname or a role). A
// weaker kind of match is used only when no stronger one found any: "who
// handles the money at acme-co" is about the company acme-co, not about
// every person whose company field says acme-co.
func matchResources(q question, rs []resourceRow) []match {
	byName := map[string][]*resourceRow{}
	for i := range rs {
		byName[strings.ToLower(rs[i].Name)] = append(byName[strings.ToLower(rs[i].Name)], &rs[i])
	}
	var out []match
	seen := map[string]bool{}
	add := func(r *resourceRow, how, field string) {
		if seen[r.ID] {
			return
		}
		seen[r.ID] = true
		out = append(out, match{res: r, how: how, field: field})
	}
	// 1. the name, whole
	for _, t := range q.tokens {
		for _, r := range byName[t] {
			add(r, MatchName, "")
		}
	}
	// a name part ("alice" for alice-ng) only when no whole name matched
	if len(out) == 0 {
		for _, t := range q.tokens {
			if len([]rune(t)) < 4 || skipWords[t] {
				continue
			}
			for i := range rs {
				name := strings.ToLower(rs[i].Name)
				if name != t && slices.Contains(strings.FieldsFunc(name, isNameSep), t) {
					add(&rs[i], MatchName, "")
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	// 2. a typo: the closest name within the allowed distance
	for _, t := range q.tokens {
		if skipWords[t] || len([]rune(t)) < 4 {
			continue
		}
		best, bestD := []*resourceRow(nil), 3
		for i := range rs {
			d := osa(t, strings.ToLower(rs[i].Name))
			if d > typoBudget(t) || d > bestD {
				continue
			}
			if d < bestD {
				best, bestD = nil, d
			}
			best = append(best, &rs[i])
		}
		for _, r := range best {
			add(r, MatchTypo, "")
		}
	}
	if len(out) > 0 {
		return out
	}
	// 3. a field value: a nickname, a role ("the CFO", "our lawyer")
	low := " " + strings.Join(q.tokens, " ") + " "
	for i := range rs {
		for _, f := range rs[i].Fields {
			v := strings.ToLower(strings.TrimSpace(f.Value))
			if v == "" || skipWords[v] || len([]rune(v)) < 3 {
				continue
			}
			if strings.Contains(low, " "+v+" ") {
				add(&rs[i], MatchField, f.Name)
				break
			}
		}
	}
	return out
}

// isNameSep splits a resource name into its parts.
func isNameSep(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' }

// typoBudget is how many letters a word may be off a name: one for a
// short word, two from eight letters on.
func typoBudget(w string) int {
	if len([]rune(w)) >= 8 {
		return 2
	}
	return 1
}

// osa is the optimal string alignment distance of a and b: insertions,
// deletions, substitutions and swaps of two neighbouring letters (lumne
// → lumen is one).
func osa(a, b string) int {
	x, y := []rune(a), []rune(b)
	if d := len(x) - len(y); d > 2 || d < -2 {
		return 3
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
