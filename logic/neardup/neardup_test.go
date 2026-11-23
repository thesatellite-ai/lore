package neardup

import (
	"fmt"
	"strings"
	"testing"
)

// TestFindPairsSameTitleAndSimilarBody is devco D-724: a repo's rule
// adopted into a folder that already holds a hand-written rule of the same
// title (or the same text) is listed as a near-duplicate of it; other
// tables, other text and the same id are not.
func TestFindPairsSameTitleAndSimilarBody(t *testing.T) {
	existing := []Item{
		{Table: "rules", ID: "rul_old1", Title: "Use portless for dev URLs", Body: "Every dev server is reached through portless names."},
		{Table: "rules", ID: "rul_old2", Title: "Commit style", Body: "write the subject as what a user sees now, then the area in brackets"},
		{Table: "decisions", ID: "dec_old1", Title: "Pick a queue", Body: "we chose the nats queue for background jobs"},
		{Table: "memories", ID: "mem_old1", Body: "the staging database restarts every night at two so long jobs fail then"},
	}
	incoming := []Item{
		{Table: "rules", ID: "rul_new1", Title: "use  PORTLESS for dev urls!", Body: "different words entirely here"},
		{Table: "rules", ID: "rul_new2", Title: "How to write commits", Body: "Write the subject as what a user sees now, then the area in brackets."},
		{Table: "rules", ID: "rul_new3", Title: "Pick a queue", Body: "we chose the nats queue for background jobs"}, // a decision's twin, but a rule
		{Table: "memories", ID: "mem_new1", Body: "The staging database restarts every night at two, so long jobs fail then!"},
		{Table: "memories", ID: "mem_new2", Body: "lunch is at noon"},
		{Table: "rules", ID: "rul_old2", Title: "Commit style", Body: "an update of the same row"},
	}
	got := Find(incoming, existing)
	var lines []string
	for _, m := range got {
		lines = append(lines, fmt.Sprintf("%s~%s %s %d", m.New.ID, m.Existing.ID, m.Why, m.Percent))
	}
	want := []string{
		"rul_new1~rul_old1 same title 100",
		"rul_new2~rul_old2 similar body 100",
		"mem_new1~mem_old1 similar body 100",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("pairs:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// TestFindBodyThreshold: bodies sharing fewer than Threshold of their
// words, or too short to say, never pair.
func TestFindBodyThreshold(t *testing.T) {
	ex := []Item{{Table: "rules", ID: "a", Body: "one two three four five six seven eight nine ten"}}
	cases := map[string]bool{
		"one two three four five six seven eight nine ten eleven": true,  // 10/11
		"one two three four five six seven eight x y z":           false, // 8/13
		"one two three": false, // too short
	}
	for b, want := range cases {
		got := len(Find([]Item{{Table: "rules", ID: "b", Body: b}}, ex)) == 1
		if got != want {
			t.Errorf("%q: paired %v, want %v", b, got, want)
		}
	}
}

// TestFindManyRowsFast: a folder of thousands against thousands finishes
// (prefix filtering keeps it near linear).
func TestFindManyRowsFast(t *testing.T) {
	var ex, in []Item
	note := func(tag string, i int) string { // nine words of its own, three every note has
		return fmt.Sprintf("the %[1]s%[2]da %[1]s%[2]db %[1]s%[2]dc %[1]s%[2]dd %[1]s%[2]de %[1]s%[2]df %[1]s%[2]dg %[1]s%[2]dh %[1]s%[2]di of a note", tag, i)
	}
	for i := range 4000 {
		ex = append(ex, Item{Table: "memories", ID: fmt.Sprintf("e%d", i), Body: note("e", i)})
		in = append(in, Item{Table: "memories", ID: fmt.Sprintf("n%d", i), Body: note("n", i)})
	}
	in = append(in, Item{Table: "memories", ID: "dup", Body: ex[17].Body})
	got := Find(in, ex)
	if len(got) != 1 || got[0].New.ID != "dup" || got[0].Existing.ID != "e17" {
		t.Fatalf("got %d pairs, want only dup~e17", len(got))
	}
}

// TestHeadline: a row without a title is named by its summary, else by its
// body's first line when that says something (a rule "Use portless for dev
// URLs." twice is the owner's D-724 case).
func TestHeadline(t *testing.T) {
	cases := []struct{ title, summary, body, want string }{
		{"A title", "a summary", "a body line here", "A title"},
		{"", "use portless", "a body line here", "use portless"},
		{"", "ok", "a body line here", "a body line here"}, // D-926: a one-word summary names nothing
		{"", "", "Use portless for dev URLs.\nEvery server has a name.", "Use portless for dev URLs."},
		{"", "", "## Notes\nsomething", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		if got := Headline(c.title, c.summary, c.body); got != c.want {
			t.Errorf("Headline(%q, %q, %q) = %q, want %q", c.title, c.summary, c.body, got, c.want)
		}
	}
	a := Item{Table: "rules", ID: "rul_a", Title: Headline("", "", "Use portless for dev URLs."), Body: "Use portless for dev URLs."}
	b := Item{Table: "rules", ID: "rul_b", Title: Headline("", "", "use portless for dev urls\nso every server has a name"), Body: "use portless for dev urls\nso every server has a name"}
	if m := Find([]Item{b}, []Item{a}); len(m) != 1 || m[0].Why != SameTitle {
		t.Fatalf("two portless rules: %+v", m)
	}
}

// TestWithinPairsRewritesOnce is devco D-926: an import and its rewrite a
// month later, both live, pair once with the older row as Existing:
// through source_ref (whatever the wording), the same title, or the same
// text; an unrelated row and a source_ref naming no row of the set do not.
func TestWithinPairsRewritesOnce(t *testing.T) {
	items := []Item{ // oldest first
		{Table: "rules", ID: "rul_a", Body: "Money amounts are stored as integer cents and never as floats in any service or table."},
		{Table: "hotfixes", ID: "hfx_a", Title: "Staging deploy needs a cache flush", Body: "After a staging deploy run the flush job."},
		{Table: "memories", ID: "mem_a", Body: "The invoice export runs at two at night and takes about forty minutes on the big accounts."},
		{Table: "memories", ID: "mem_x", Body: "lunch is at noon on fridays for the whole team", SourceRef: "notes/import.md:12"},
		{Table: "rules", ID: "rul_b", Body: "Store every money amount as integer cents in every service and table; never as floats.", SourceRef: "rul_a"},
		{Table: "hotfixes", ID: "hfx_b", Title: "staging deploy needs a CACHE flush!", Body: "Flush the cache after each staging deploy, otherwise prices are stale."},
		{Table: "memories", ID: "mem_b", Body: "The invoice export runs at two at night and takes about forty minutes for the big accounts."},
		{Table: "rules", ID: "rul_c", Body: "Every endpoint checks the caller's role before it reads a row.", SourceRef: "rul_gone"},
	}
	var lines []string
	for _, m := range Within(items) {
		lines = append(lines, fmt.Sprintf("%s>%s %s", m.Existing.ID, m.New.ID, m.Why))
	}
	want := []string{"rul_a>rul_b rewrite of", "hfx_a>hfx_b same title", "mem_a>mem_b similar body"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("pairs:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
