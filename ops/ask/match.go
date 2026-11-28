package ask

import (
	"maps"
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
	// nm is what the name checks read of the row (nameMemo), built on the
	// first one.
	nm *nameMemo
}

// nameMemo is what ask's name checks read of one resource, built once per
// ask and kept on the row: every ask loads its resources afresh
// (loadTyped) and reads them in one goroutine, so it needs no lock. Why
// (devco D-1036, bug hunt H-300): namesResource runs for every question
// word against every identifier of every scored row, and it rebuilt the
// names through a regexp (aliasKey) on each call; a question of twenty
// words over notes full of identifiers took 5 s where others take 0.5 s.
type nameMemo struct {
	// name and aliases are what it was built from (aliases a copy): a row
	// copied with another name or aliases (a test's fixture) builds anew.
	name    string
	aliases []string
	// names is names(): the name in lower case, then each alias key.
	names []string
	// keys is nameKey of each of names (widget_api as "widget api").
	keys []string
	// aliasKeys is aliasKey of each alias, in Aliases' order.
	aliasKeys []string
	// parts are the words of the name's key when there are two or more
	// (widget api), else nil.
	parts []string
	// named holds namesWord's answer for each word asked so far.
	named map[string]bool
}

// memo is r's nameMemo, built on the first call and again when r's name
// or aliases are no longer the ones it was built from.
func (r *resourceRow) memo() *nameMemo {
	if m := r.nm; m != nil && m.name == r.Name && slices.Equal(m.aliases, r.Aliases) {
		return m
	}
	m := &nameMemo{name: r.Name, aliases: slices.Clone(r.Aliases), names: []string{strings.ToLower(r.Name)}, named: map[string]bool{}}
	for _, a := range r.Aliases {
		k := aliasKey(a)
		m.aliasKeys = append(m.aliasKeys, k)
		if k != "" && !slices.Contains(m.names, k) {
			m.names = append(m.names, k)
		}
	}
	for _, n := range m.names {
		m.keys = append(m.keys, nameKey(n))
	}
	if ws := keyWords(nameKey(r.Name)); len(ws) > 1 {
		m.parts = ws
	}
	r.nm = m
	return m
}

// names are r's name and each alias, as question tokens read them (lower
// case; a several-word alias as its words joined by one space). The slice
// is shared (nameMemo): callers only read it.
func (r *resourceRow) names() []string { return r.memo().names }

// namesWord reports whether word w is (a typo of) r's name or an alias,
// or a word of a name the words split (api of widget_api); the answer is
// kept per word (nameMemo), so a question word costs one check per
// resource per ask, whatever the number of rows and identifiers.
func (r *resourceRow) namesWord(w string) bool {
	m := r.memo()
	if v, ok := m.named[w]; ok {
		return v
	}
	v := slices.Contains(m.parts, w)
	for i, n := range m.names {
		if v {
			break
		}
		// an alias (names[1:]) is a typo only from four letters on: cto is
		// not the alias CFO
		v = w == n || ((i == 0 || len([]rune(w)) >= 4) && osa(w, n) <= typoBudget(w))
	}
	m.named[w] = v
	return v
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

// named says the question names the resource: its name or an alias said
// whole, not a typo of one. Why (devco D-975, bug hunt H-234): a typo is
// found by distance, and a common question word (webhook) is one letter
// off a code_repo named webhooks; a resource so found only touches the
// question (its card prints as one line under the rows, the rows need not
// be about it), unless the question asks everything about it.
func (m match) named() bool { return m.whole && m.how != MatchTypo }

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
		nm := rs[i].memo()
		k := nm.keys[0]
		byName[k] = append(byName[k], &rs[i])
		if nm.parts != nil {
			longNames = append(longNames, i)
		}
		for _, k := range nm.aliasKeys {
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
		if strings.Contains(said, " "+rs[i].memo().keys[0]+" ") {
			add(&rs[i], MatchName, "", "")
		}
	}
	// sorted: a map's order changes from run to run, and the first match
	// leads the answer (devco:D-979, bug hunt H-238)
	for _, k := range slices.Sorted(maps.Keys(byAlias)) {
		if k == "" || skipWords[k] || !strings.Contains(said, " "+k+" ") {
			continue
		}
		for _, r := range byAlias[k] {
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
				for _, k := range rs[i].memo().aliasKeys {
					if slices.Contains(strings.FieldsFunc(k, isNameSep), t) {
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
		for _, k := range rs[i].memo().aliasKeys {
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
