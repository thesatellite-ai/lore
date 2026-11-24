package ask

// keptwords_test.go — bug hunt H-214: a warm lore ask copies its rows out
// of the kept corpus, so the word sets score and markReplaced read must be
// built on the kept row (once), not on each call's copy; and restates,
// which stops early, answers as the full count did.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

func TestKeptCorpusRowsCarryTheirWordSets(t *testing.T) {
	txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	body := "Ferry plan: the harbour timetable moves to the winter hours"
	lines := [][]value.Value{{txt("row"), txt("memories"), txt("mem_kw1"), txt("me/life"), txt(""), txt(body), txt("2026-11-01T00:00:00.000000000Z"), txt("0"), txt(""), txt(""), txt(body), txt("2026-11-01T00:00:00Z"), txt("2026-11-01T00:00:00Z")}}
	c := corpusOf(lines)
	copied := c.rows[c.at["mem_kw1"]] // what each call takes (ask.go)
	if copied.words == nil || copied.first == nil || copied.content == nil || !copied.labeled {
		t.Fatalf("a copy of a kept row has no word sets (words %v, first %v, content %v, labeled %v): every call splits the text again", copied.words != nil, copied.first != nil, copied.content != nil, copied.labeled)
	}
	if !copied.words["harbour"] || !copied.first["timetable"] || strings.Join(copied.label, " ") != "ferry plan" {
		t.Fatalf("kept word sets are wrong: words %v first %v label %v", copied.words, copied.first, copied.label)
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
		if skipWords[w] {
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
