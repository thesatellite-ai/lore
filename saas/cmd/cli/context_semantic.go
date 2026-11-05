// context_semantic.go — step 5 of `lore context` (CONTEXT_BUDGET 6.8,
// line 659): rules with activation: semantic are matched on the words of
// the path, task or prompt against their applies_to_description (plain
// word match now; embeddings later, SEARCH_INDEX phase 2). They are never
// pinned, so this is the only way they reach an agent besides search.
//
//	words(path | task | prompt) ─► semantic rules, best match first,
//	  at most contextNotesMax ─► add(k, "semantic <matched words>")
package main

import (
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/schema"
)

// renderFieldAppliesTo is a rule's semantic description.
const renderFieldAppliesTo = "applies_to_description"

// semanticRulePicks adds the semantic rules whose description shares a
// word with words, best first, capped at contextNotesMax.
func semanticRulePicks(rows []knowledgeRow, words []string, add func(knowledgeRow, string)) {
	type scored struct {
		k       knowledgeRow
		matched []string
	}
	var cand []scored
	for _, k := range rows {
		if k.kind != "rule" || renderField(k.r.Row, renderFieldActivation) != schema.RuleActivationSemantic {
			continue
		}
		desc := strings.ToLower(renderField(k.r.Row, renderFieldAppliesTo))
		if desc == "" {
			continue
		}
		var m []string
		for _, w := range words {
			if contextfit.Score(desc, []string{w}) > 0 {
				m = append(m, w)
			}
		}
		if len(m) > 0 {
			cand = append(cand, scored{k, m})
		}
	}
	sort.SliceStable(cand, func(i, j int) bool { return len(cand[i].matched) > len(cand[j].matched) })
	for i, c := range cand {
		if i >= contextNotesMax {
			break
		}
		add(c.k, "semantic "+strings.Join(c.matched, " "))
	}
}
