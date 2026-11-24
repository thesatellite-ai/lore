package wire_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// punctQueries are plain words around FTS5 punctuation a person types
// without meaning any syntax: `alice (person)` failed every search with
// "fts5: syntax error near ..." (devco D-798, P-14). Each must find the
// row holding "quokka".
var punctQueries = []string{
	`quokka (person)`,
	`quokka)`,
	`(quokka`,
	`"quokka`,
	`quokka^`,
	`{quokka}`,
	`quokka AND`,
	`quokka OR (`,
	`quokka NOT`,
	`quokka (zzz`,
}

// syntaxKept is FTS5 syntax lore documents (a phrase, a prefix, AND
// between words): run as written, it still finds the row.
var syntaxKept = []string{`"quokka"`, `quok*`, `quokka AND quokka`}

// noWordQueries hold no word to search for: they keep FTS5's refusal and
// its hint (D-475).
var noWordQueries = []string{`(`, `"`, `AND`, `a OR`, `NEAR(`}

// colonQueries hold a word with a ':' the way task and script names are
// written (lint:quokka, quokka:all): the colon separates words, so the
// row holding "quokka" (and no "lint" next to it) is found (devco D-964,
// H-223). Before, FTS5 got each such word as one phrase and found
// nothing.
var colonQueries = []string{`lint:quokka`, `quokka:all`, `lint:quokka exempt`, `zzz:yyy:quokka`}

// kindQueries is the short set each `<noun>.search` runs (they share one
// matcher, opsmemory.Match); `lore search` runs every query above.
var kindQueries, kindNoWord = []string{`quokka (person)`, `"quokka`, `quok*`, `lint:quokka`}, []string{`(`}

// TestEverySearchReadsPunctuatedWords (devco D-798, P-14) walks `lore
// search` and every registered `<noun>.search`: a query of words around a
// stray parenthesis, quote, brace, ^ or dangling operator finds the row
// holding the words, documented syntax still works as written, and a
// query with no word at all is still E_INVALID_INPUT. Before, every
// search returned the FTS5 syntax error for `quokka (person)`. A word
// with a colon (lint:quokka) finds the row too (devco D-964, H-223).
func TestEverySearchReadsPunctuatedWords(t *testing.T) {
	h := opstest.New(t)
	on := h.ID(h.MustYes("memory.add", `{"body":"a row to comment on"}`))
	h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Quinn"`, "handle": `"quinn"`, "stable_key": `"human:quinn@example.com"`}) // a handoff is for someone
	var searches []string
	for _, e := range h.App.Registry().Entries() {
		noun, isSearch := strings.CutSuffix(e.Spec().Name, ".search")
		if !isSearch || noun == "search" {
			continue
		}
		add, ok := searchMoreAdds[noun]
		if !ok {
			t.Errorf("%s search: no add input in searchMoreAdds; add one so its query reading is checked", noun)
			continue
		}
		add = strings.ReplaceAll(add, "%ON%", on)
		h.MustYes(noun+".add", strings.ReplaceAll(add, "%N%", strconv.Itoa(len(searches)))+"}")
		searches = append(searches, noun+".search")
	}
	check := func(op string, found, refused []string) {
		for _, q := range found {
			in := `{"query":` + strconv.Quote(q) + `}`
			res, err := h.Run(op, in)
			if err != nil {
				t.Errorf("%s %s: %v, want the quokka row", op, in, err)
				continue
			}
			// "(no matches)" echoes the query, which holds the word too
			if txt := opstest.Text(res); !strings.Contains(txt, "quokka") || strings.Contains(txt, "(no matches)") {
				t.Errorf("%s %s: no quokka row:\n%s", op, in, txt)
			}
		}
		for _, q := range refused {
			h.Fails(op, `{"query":`+strconv.Quote(q)+`}`, errs.InvalidInput)
		}
	}
	for _, op := range searches {
		check(op, kindQueries, kindNoWord)
	}
	check("search.run", append(append(append([]string{}, punctQueries...), syntaxKept...), colonQueries...), noWordQueries)
}
