package ask

// keptwords_test.go — bug hunt H-214: a warm lore ask copies its rows out
// of the kept corpus, so the word sets score and markReplaced read must be
// shared with the kept row (built once), not built on each call's copy;
// H-219: built when first read, never all at the parse; and restates,
// which stops early, answers as the full count did.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestKeptCorpusRowsShareWordSetsBuiltOnUse (bug hunt H-219): parsing a
// corpus splits no row's text (a cold lore ask, the corpus new, paid ~70 ms
// on a 6,000-row store splitting rows it never read), and a word set one
// call builds on its copy of a kept row is the set the next call's copy
// reads, not built again (H-214's warm win kept).
func TestKeptCorpusRowsShareWordSetsBuiltOnUse(t *testing.T) {
	txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	body := "Ferry plan: the harbour timetable moves to the winter hours"
	lines := [][]value.Value{{txt("row"), txt("memories"), txt("mem_kw2"), txt("me/life"), txt(""), txt(body), txt("2026-11-02T00:00:00.000000000Z"), txt("0"), txt(""), txt(""), txt(body), txt("2026-11-02T00:00:00Z"), txt("2026-11-02T00:00:00Z")}}
	c := corpusOf(lines)
	kept := c.rows[c.at["mem_kw2"]]
	if kept.split == nil {
		t.Fatal("a kept corpus row has no shared split: each call's copy builds its word sets again")
	}
	if s := kept.split; s.words.built || s.first.built || s.content.built || s.label.built || kept.words != nil || kept.first != nil || kept.content != nil || kept.labeled {
		t.Fatalf("parsing the corpus split the row's text (words %v, first %v, content %v, label %v): a cold ask pays for every row of the store", s.words.built, s.first.built, s.content.built, s.label.built)
	}

	one := c.rows[c.at["mem_kw2"]] // the first call's copy (ask.go)
	words, first, content, label := one.tokens(), one.firstTokens(), contentOf(&one), labelOf(&one)
	if !words["harbour"] || !first["timetable"] || len(content) == 0 || strings.Join(label, " ") != "ferry plan" {
		t.Fatalf("word sets are wrong: words %v first %v content %v label %v", words, first, content, label)
	}

	two := c.rows[c.at["mem_kw2"]] // the next call's copy
	if two.words != nil || two.first != nil || two.content != nil || two.labeled {
		t.Fatal("a copy carries the first copy's own memo fields: the fixture does not test the shared split")
	}
	same := func(a, b map[string]bool) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }
	if !same(two.tokens(), words) || !same(two.firstTokens(), first) || &contentOf(&two)[0] != &content[0] || &labelOf(&two)[0] != &label[0] {
		t.Error("the next call's copy split the row's text again instead of reading the set the first call built")
	}

	// a row with no label words keeps that answer too (a built nil label)
	plain := corpusOf([][]value.Value{{txt("row"), txt("memories"), txt("mem_kw3"), txt("me/life"), txt(""), txt("no colon here at all"), txt("2026-11-02T00:00:00.000000000Z"), txt("0"), txt(""), txt(""), txt("no colon here at all"), txt("2026-11-02T00:00:00Z"), txt("2026-11-02T00:00:00Z")}})
	p := plain.rows[0]
	if labelOf(&p) != nil || !plain.rows[0].split.label.built {
		t.Error("a row with no label words: the empty label is not kept for the next copy")
	}
}

// fullRestates is restates as it was before the early stop: every word
// of older counted.
func fullRestates(newer, older *textRow) bool {
	if sameLabel(newer, older) {
		return true
	}
	nt := newer.tokens()
	total, held := 0, 0
	for w := range older.tokens() {
		if skipWords[w] || isStemKey(w) {
			continue
		}
		total++
		if nt[w] || nt[w+"s"] || (len(w) > 3 && nt[strings.TrimSuffix(w, "s")]) {
			held++
		}
	}
	return 2*held >= total
}

func TestRestatesStopsEarlyWithTheSameAnswer(t *testing.T) {
	words := []string{"import", "export", "billing", "invoice", "webhook", "queue", "retry", "cache", "admin", "login", "token", "session", "the", "and", "runs", "steps"}
	var rows []*textRow
	for i := range 60 {
		var b strings.Builder
		for k := range 3 + i%9 {
			fmt.Fprintf(&b, "%s ", words[(i*7+k*k*3)%len(words)])
		}
		rows = append(rows, &textRow{Body: b.String()})
	}
	rows = append(rows, &textRow{Body: ""}, &textRow{Body: "the and"})
	yes := 0
	for _, a := range rows {
		for _, o := range rows {
			want := fullRestates(a, o)
			if got := restates(a, o); got != want {
				t.Fatalf("restates(%q, %q) = %v, the full count says %v", a.Body, o.Body, got, want)
			}
			if want {
				yes++
			}
		}
	}
	if yes == 0 || yes == len(rows)*len(rows) {
		t.Fatalf("the cases test one answer only (%d of %d restate)", yes, len(rows)*len(rows))
	}
}
