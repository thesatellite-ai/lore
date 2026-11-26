package ask

// candidates_test.go — bug hunt H-245 (devco D-998): after the full rows
// ask lists search's next candidates one line each, resource cards stand
// only in that list while a row answers, and the list counts in
// --max-chars.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// manyRowsFixture is shopFixture with twelve more short rows holding the
// question's words, so cap leaves rows out and --top leaves rows past it.
func manyRowsFixture() data {
	d := shopFixture()
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	for i := range 12 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_%02d", i), Place: "kb/app",
			Body: fmt.Sprintf("Shop import note %d: the import workflow for a workspace retries page %d twice before it stops.", i, i), At: at.Add(-time.Duration(i) * time.Hour)})
	}
	return d
}

func TestCandidatesAfterFullRows(t *testing.T) {
	t.Parallel()
	q := parseQuestion("can two shop imports run at the same time for the same workflow and workspace", now)
	q.home = "kb/app"
	for _, maxChars := range []int{600, 1200, DefaultMaxChars} {
		o := build(q, manyRowsFixture(), 0, maxChars)
		text := o.Text()
		for _, r := range o.Ranked {
			if r.Kind == KindResource {
				t.Errorf("max %d: a resource card is a full item while rows answer: %+v\n%s", maxChars, o.Ranked, text)
			}
		}
		if n := len(o.Candidates); n < min(minCandidates, 1+maxChars/DefaultMaxChars*4) || n > maxCandidates {
			t.Errorf("max %d: %d candidates, want %d-%d:\n%s", maxChars, n, minCandidates, maxCandidates, text)
		}
		if len(o.body()) > maxChars {
			t.Errorf("max %d: the full items and the list take %d characters:\n%s", maxChars, len(o.body()), text)
		}
		full, cards := map[string]bool{}, 0
		for _, r := range o.Ranked {
			full[r.ID] = true
		}
		for _, c := range o.Candidates {
			if full[c.ID] {
				t.Errorf("max %d: %s is both a full item and a candidate", maxChars, c.ID)
			}
			if c.ID == "" || c.Title == "" || c.Line == "" || c.Table == "" {
				t.Errorf("max %d: a candidate lacks a field: %+v", maxChars, c)
			}
			if !strings.Contains(text, "  "+c.ID+" ("+c.Table+") ") {
				t.Errorf("max %d: %s is not a line of the text:\n%s", maxChars, c.ID, text)
			}
			if c.Kind == KindResource {
				cards++
			}
		}
		if cards > 0 && maxChars < DefaultMaxChars {
			// the cards hold fewer of the question's words than every
			// note: they stand after them, past the list's end
			t.Errorf("max %d: a card stands before rows holding more words:\n%s", maxChars, text)
		}
		if !strings.Contains(text, CandidatesHeading) {
			t.Errorf("max %d: no candidate heading:\n%s", maxChars, text)
		}
	}
	// with only a few rows the cards are in the list, after the rows
	// holding more words, each line its purpose
	o := build(q, shopFixture(), 0, 0)
	cards := 0
	for _, c := range o.Candidates {
		if c.Kind == KindResource {
			cards++
			if !strings.HasPrefix(c.Line, "purpose: ") {
				t.Errorf("card line %q is not its purpose", c.Line)
			}
		}
	}
	if cards == 0 || slices.ContainsFunc(o.Ranked, func(r Ranked) bool { return r.Kind == KindResource }) {
		t.Errorf("cards: %d candidate lines, ranked %+v; want them in the list alone:\n%s", cards, o.Ranked, o.Text())
	}
}

// TestCandidatesNextInSearchOrder: the list's rows are lore search's
// next rows after the ones printed in full, in search's order (H-270,
// devco D-1008: before, the rows cap left out in the answer's order).
func TestCandidatesNextInSearchOrder(t *testing.T) {
	t.Parallel()
	q := parseQuestion("can two shop imports run at the same time for the same workflow and workspace", now)
	q.home = "kb/app"
	o := build(q, manyRowsFixture(), 0, 900)
	printed := map[string]bool{}
	for _, r := range o.Ranked {
		printed[r.ID] = true
	}
	var order []string // search's order without the rows printed in full
	for _, id := range plainOrder(q, manyRowsFixture().rows, nil) {
		if !printed[id] {
			order = append(order, id)
		}
	}
	var rows []string
	for _, c := range o.Candidates {
		if c.Kind == KindRow {
			rows = append(rows, c.ID)
		}
	}
	if len(rows) == 0 || len(rows) > len(order) {
		t.Fatalf("rows %v; search's next rows %v\n%s", rows, order, o.Text())
	}
	for i, id := range rows {
		if order[i] != id {
			t.Errorf("candidate rows %v, want the next of search's order %v\n%s", rows, order, o.Text())
			break
		}
	}
}

// TestCandidatesJSONAndDeterministic: --json carries the list; the same
// question twice gives the same bytes; a who-is answer keeps its card.
func TestCandidatesJSONAndDeterministic(t *testing.T) {
	t.Parallel()
	q := parseQuestion("which workflow stamps the caller on a shop import run", now)
	a, _ := json.Marshal(build(q, manyRowsFixture(), 0, 0))
	b, _ := json.Marshal(build(q, manyRowsFixture(), 0, 0))
	if string(a) != string(b) {
		t.Errorf("two runs differ:\n%s\n%s", a, b)
	}
	var got struct {
		Candidates []struct {
			Line string `json:"line"`
			Rank int    `json:"rank"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(a, &got); err != nil || len(got.Candidates) == 0 || got.Candidates[0].Line == "" || got.Candidates[0].Rank == 0 {
		t.Errorf("--json has no candidates with a line: %v %s", err, a)
	}
	who := build(parseQuestion("who is shop_app", now), shopFixture(), 0, 0)
	if len(who.Resources) != 1 || len(who.Candidates) != 0 {
		t.Errorf("who-is: resources %+v candidates %+v, want the card itself", who.Resources, who.Candidates)
	}
}

func TestBestLine(t *testing.T) {
	t.Parallel()
	text := "Intro sentence. The import retries a page twice. Unrelated words here.\n\n" + strings.Repeat("x", 300) + " workspace import retries."
	line, held := bestLine(text, []string{"import", "retries", "page"}, 160)
	if line != "The import retries a page twice." || held != 3 {
		t.Errorf("bestLine = %q, %d", line, held)
	}
	long := strings.Repeat("filler ", 40) + "the answer names ErrImportRunning here " + strings.Repeat("tail ", 40)
	got := clipAround(long, []string{"errimportrunning"}, 80)
	if !strings.Contains(got, "ErrImportRunning") || len([]rune(got)) > 81 {
		t.Errorf("clipAround = %q", got)
	}
}
