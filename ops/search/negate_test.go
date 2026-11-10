package search_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestSearchNegatedWords: a term starting with '-' leaves out the rows
// holding that word (FTS5 NOT), wherever it stands in the query, in every
// search path: plain full text, with a metadata condition, with a resource
// field condition, and the trashed rows of --include-deleted. A '-' inside
// a word (well-known, x-ray) keeps its meaning, a negated hyphenated word
// or phrase is one negation, and a query of only negated words is refused
// with a hint naming a query that works.
func TestSearchNegatedWords(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"env","type":"text"}`)
	fri := h.ID(h.MustYes(memory.OpAdd, `{"body":"deploy on friday is risky","meta":["env=prod"]}`))
	mon := h.ID(h.MustYes(memory.OpAdd, `{"body":"deploy on monday","meta":["env=prod"]}`))
	xray := h.ID(h.MustYes(memory.OpAdd, `{"body":"well-known x-ray deploy"}`))
	call := h.ID(h.MustYes(memory.OpAdd, `{"body":"deploy while on call"}`))
	gone := h.ID(h.MustYes(memory.OpAdd, `{"body":"deploy friday ritual"}`))
	goneMon := h.ID(h.MustYes(memory.OpAdd, `{"body":"deploy monday ritual"}`))
	for _, id := range []string{gone, goneMon} {
		h.MustYes(memory.OpDelete, `{"id":"`+id+`"}`)
	}
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"region","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub_eu","set":["region=eu"]}`)

	all := []string{fri, mon, xray, call}
	cases := []struct {
		name, in string
		want     []string // exactly these ids, in any order
	}{
		{"plain word", `{"query":"deploy"}`, all},
		{"negated word", `{"query":"deploy -friday"}`, []string{mon, xray, call}},
		{"negated word first", `{"query":"-friday deploy"}`, []string{mon, xray, call}},
		{"two negated words", `{"query":"deploy -friday -monday"}`, []string{xray, call}},
		{"negated prefix", `{"query":"deploy -fri*"}`, []string{mon, xray, call}},
		{"negated phrase", `{"query":"deploy -\"on call\""}`, []string{fri, mon, xray}},
		{"negated hyphenated word", `{"query":"deploy -x-ray"}`, []string{fri, mon, call}},
		{"hyphen inside a word is no negation", `{"query":"well-known"}`, []string{xray}},
		{"hyphen inside a word, second", `{"query":"x-ray"}`, []string{xray}},
		{"lone dash is no negation", `{"query":"deploy -"}`, all},
		{"with OR on the left", `{"query":"friday OR monday -risky"}`, []string{mon}},
		{"meta condition and negated word", `{"query":"env:prod -friday"}`, []string{mon}},
		{"meta condition, word and negated word", `{"query":"env:prod deploy -monday"}`, []string{fri}},
		{"field condition and negated word", `{"query":"region:eu -zzz"}`, nil}, // resources only: checked below
		{"trash: negated word drops a trashed row", `{"query":"deploy ritual -friday","include_deleted":true}`, []string{goneMon}},
		{"trash: negated word drops every trashed row", `{"query":"deploy -ritual","include_deleted":true}`, all},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := hitIDs(t, h, c.in)
			if c.name == "field condition and negated word" {
				if len(got) != 1 || !strings.HasPrefix(got[0], "res_") {
					t.Fatalf("got %v, want the one eu resource", got)
				}
				return
			}
			if !sameSet(got, c.want) {
				t.Fatalf("got %v\nwant %v", got, c.want)
			}
		})
	}

	for _, q := range []string{"-friday", "-friday -monday", `-"on call"`} {
		msg := h.Fails(search.OpRun, `{"query":`+jsonString(q)+`}`, errs.InvalidInput)
		if !strings.Contains(msg, "a word to search for") {
			t.Errorf("%s: %s", q, msg)
		}
	}
	_, err := h.Run(search.OpRun, `{"query":"-friday"}`)
	if hint := errs.EnvelopeOf(err).Error.Hint; hint != `add a word to search for: lore search "deploy -friday"` {
		t.Errorf("hint %q", hint)
	}
}

// sameSet reports whether a and b hold the same ids.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		if seen[x] == 0 {
			return false
		}
		seen[x]--
	}
	return true
}

// jsonString is s as a JSON string literal (quotes escaped).
func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
