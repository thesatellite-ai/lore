package ask

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/fuzzyname"
)

// How a resource was found from the question (Resource.Match).
const (
	MatchName  = "name"  // the question names it: alice-ng, or one part of the name of 4+ letters (alice)
	MatchAlias = "alias" // the question says one of its aliases (lore resource alias add): Bobby, the CFO, Robert Kim
	MatchTypo  = "typo"  // a word one or two letters off its name or an alias: alise-ng, lumne, Boby
	MatchField = "field" // a word is one of its field values: a nickname (Bobby), a role (CFO)
)

// resourceRow is one resource as the lens holds it.
type resourceRow struct {
	ID, Place, Kind, Name, Updated string
	// Aliases are its other names (resources.aliases, devco D-817).
	Aliases []string
	// Fields in the kind's order of the stored object (name → value).
	Fields []fieldValue
	// History is the field history, oldest first.
	History []historyEntry
}

// names are r's name and each alias, as question tokens read them (lower
// case; a several-word alias as its words joined by one space).
func (r *resourceRow) names() []string {
	out := []string{strings.ToLower(r.Name)}
	for _, a := range r.Aliases {
		if k := aliasKey(a); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
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
	// said are the question's words that found it when they are not one
	// of its whole names: one part of its name or of an alias ("widget"
	// for widget_api), a field value ("cfo"). A row that says them is
	// about it as a row that says its name is (subjectOf), and loadRows
	// reads those rows. Why (devco D-949, public issue #157): "how does
	// the widget import work" matched the code_repo widget_api by the
	// part widget, and only rows naming widget_api in full could answer,
	// so the notes about the widget import were left out and the answer
	// was three resource cards.
	said []string
	// whole says the question names it: its name, an alias or a typo of
	// one, said whole. False when a part of its name (feed of feed_api)
	// or a field value found it: the question is then not about it, and
	// under rows it prints as one short line (Resource.brief, devco
	// D-963).
	whole bool
}

// nameKey is a resource name as the question's tokens read it (aliasKey):
// widget_api is "widget api", since a question word never holds an
// underscore; alice-ng stays alice-ng.
func nameKey(name string) string { return aliasKey(name) }

// keyWords are the words of a name or alias key ("widget api" → widget,
// api).
func keyWords(k string) []string { return strings.Fields(k) }

// matchResources finds the resources a question is about: by name or
// alias first (a token is the name, a word of an alias or the whole of a
// several-word alias, else one part of a name or alias), then by a name or
// alias one or two letters off (a typo, through a trigram index), then by
// a field value (a nickname or a role kept as a field). A weaker kind of
// match is used only when no stronger one found any: "who handles the
// money at acme-co" is about the company acme-co, not about every person
// whose company field says acme-co.
func matchResources(q question, rs []resourceRow) []match {
	byName := map[string][]*resourceRow{}
	byAlias := map[string][]*resourceRow{}
	var longNames []int // names of several words as tokens (widget_api)
	for i := range rs {
		k := nameKey(rs[i].Name)
		byName[k] = append(byName[k], &rs[i])
		if len(keyWords(k)) > 1 {
			longNames = append(longNames, i)
		}
		for _, a := range rs[i].Aliases {
			k := aliasKey(a)
			byAlias[k] = append(byAlias[k], &rs[i])
		}
	}
	var out []match
	at := map[string]int{}
	add := func(r *resourceRow, how, field, said string) {
		if i, seen := at[r.ID]; seen {
			if said != "" && out[i].how == how && !slices.Contains(out[i].said, said) {
				out[i].said = append(out[i].said, said)
			}
			return
		}
		at[r.ID] = len(out)
		// a name, alias or typo of one said whole names it; a part of a
		// name or a field value only touches it (match.whole)
		m := match{res: r, how: how, field: field, whole: said == "" && how != MatchField}
		if said != "" {
			m.said = []string{said}
		}
		out = append(out, m)
	}
	// 1. the name, whole; then an alias, whole: one word (Bobby) or the
	// words of a longer one in a row ("robert kim", "chief of staff"); a
	// name the tokens split (widget_api: widget api) is whole when its
	// words stand in a row
	said := " " + strings.Join(q.tokens, " ") + " "
	for _, t := range q.tokens {
		for _, r := range byName[t] {
			add(r, MatchName, "", "")
		}
	}
	for _, i := range longNames {
		if strings.Contains(said, " "+nameKey(rs[i].Name)+" ") {
			add(&rs[i], MatchName, "", "")
		}
	}
	for k, owners := range byAlias {
		if k == "" || skipWords[k] || !strings.Contains(said, " "+k+" ") {
			continue
		}
		for _, r := range owners {
			add(r, MatchAlias, "", "")
		}
	}
	// a name part ("alice" for alice-ng) or an alias part ("robert" for
	// Robert Kim) only when no whole name or alias matched
	if len(out) == 0 {
		for _, t := range q.tokens {
			if len([]rune(t)) < 4 || skipWords[t] {
				continue
			}
			for i := range rs {
				name := strings.ToLower(rs[i].Name)
				if name != t && slices.Contains(strings.FieldsFunc(name, isNameSep), t) {
					add(&rs[i], MatchName, "", t)
				}
				for _, a := range rs[i].Aliases {
					if slices.Contains(strings.FieldsFunc(aliasKey(a), isNameSep), t) {
						add(&rs[i], MatchAlias, "", t)
					}
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	// 2. a typo: the closest names and aliases within the allowed
	// distance, found through a trigram index (no scan of every name)
	idx := typoIndex(rs)
	for _, t := range q.tokens {
		if skipWords[t] || len([]rune(t)) < 4 {
			continue
		}
		for _, h := range idx.Near(t, typoBudget(t)) {
			add(&rs[h.Owner], MatchTypo, "", "")
		}
	}
	if len(out) > 0 {
		return out
	}
	// 3. a field value: a nickname, a role ("the CFO", "our lawyer")
	for i := range rs {
		for _, f := range rs[i].Fields {
			v := strings.ToLower(strings.TrimSpace(f.Value))
			if v == "" || skipWords[v] || len([]rune(v)) < 3 {
				continue
			}
			if strings.Contains(said, " "+v+" ") {
				add(&rs[i], MatchField, f.Name, v)
				break
			}
		}
	}
	return out
}

// typoIndex is the trigram index of every resource's name, each alias
// and each word of 4+ letters of a several-word alias (Robert Kim: robert
// kim, robert), owned by the resource's index in rs.
func typoIndex(rs []resourceRow) *fuzzyname.Index {
	idx := fuzzyname.New()
	for i := range rs {
		idx.Add(rs[i].Name, i)
		for _, a := range rs[i].Aliases {
			k := aliasKey(a)
			idx.Add(k, i)
			if ws := strings.Fields(k); len(ws) > 1 {
				for _, w := range ws {
					if len([]rune(w)) >= 4 && !skipWords[w] {
						idx.Add(w, i)
					}
				}
			}
		}
	}
	return idx
}

// aliasKey is an alias as the question's tokens read it: lower case, its
// words (reWord) joined by one space ("Dr. Robert Kim" → "dr robert kim").
func aliasKey(a string) string {
	var ws []string
	for _, w := range reWord.FindAllString(strings.ToLower(a), -1) {
		if w = strings.Trim(w, "."); w != "" {
			ws = append(ws, w)
		}
	}
	return strings.Join(ws, " ")
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

// osa is the optimal string alignment distance of a and b, at most 3
// (fuzzyname.OSA with a limit of two edits).
func osa(a, b string) int { return fuzzyname.OSA(a, b, 2) }
