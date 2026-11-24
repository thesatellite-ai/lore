package ask

import (
	"fmt"
	"strings"
	"testing"
)

// longDoc is a ~5,800-character markdown note with one fact in the middle
// section, as a design doc holds it.
func longDoc() string {
	var b strings.Builder
	b.WriteString("# Platform design notes\n\n")
	section := func(topic string) {
		fmt.Fprintf(&b, "## %s\n\n", topic)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(&b, "This part of the design covers %s in plain terms. The team reviewed the %s plan in sprint %d and agreed to keep the current layout, revisit the open points later and write down every follow-up in the tracker. ", topic, topic, i)
		}
		b.WriteString("\n\n")
	}
	for _, s := range []string{"Request routing", "Session handling", "Search indexing", "Email delivery"} {
		section(s)
	}
	b.WriteString("## Operations\n\nThe nightly backup of the billing database runs at 03:15 UTC on host vault-seven.\n\n")
	for _, s := range []string{"Feature flags", "Image resizing", "Audit trail", "Rate limiting"} {
		section(s)
	}
	return b.String()
}

// TestCapCutsLongBestRow (devco D-859, public issue #86): a best row
// longer than --max-chars is printed cut to the part that answers the
// question, marked as cut with the command that prints it whole, and the
// short rows under it that fit are still printed; the answer is never
// "(no answer)" while a row matched.
func TestCapCutsLongBestRow(t *testing.T) {
	t.Parallel()
	doc := longDoc()
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_long", Place: "repo", Body: doc, At: now.AddDate(0, 0, -1)},
		{Table: "memories", ID: "mem_short", Place: "repo", Body: "Deploys go out on Tuesdays after the database backup check passes.", At: now.AddDate(0, 0, -2)},
		{Table: "memories", ID: "mem_other", Place: "repo", Body: "The frontend uses Tailwind and the shared design tokens.", At: now.AddDate(0, 0, -3)},
	}}
	q := parseQuestion("when does the nightly backup of the billing database run", now)
	for _, maxChars := range []int{DefaultMaxChars, 1000, 200} {
		o := build(q, d, 0, maxChars)
		text := o.Text()
		if len(o.Rows) == 0 || o.Rows[0].ID != "mem_long" || strings.Contains(text, NoAnswer) {
			t.Fatalf("max %d: the long best row is not printed:\n%s", maxChars, text)
		}
		r := o.Rows[0]
		if r.FullChars != len(doc) || len(r.Body) >= len(doc) {
			t.Errorf("max %d: full_chars %d body %d, want %d and a cut body", maxChars, r.FullChars, len(r.Body), len(doc))
		}
		for _, want := range []string{"03:15 UTC on host vault-seven", "lore show --id mem_long prints it whole", "the whole row is " + fmt.Sprint(len(doc)) + " characters"} {
			if !strings.Contains(text, want) {
				t.Errorf("max %d: no %q in\n%s", maxChars, want, text)
			}
		}
		if maxChars >= 1000 {
			if len(o.Rows) < 2 || o.Rows[1].ID != "mem_short" || o.Left != 0 {
				t.Errorf("max %d: the short row that fits is dropped: rows %d left %d\n%s", maxChars, len(o.Rows), o.Left, text)
			}
			if len(o.body()) > maxChars {
				t.Errorf("max %d: the answer is %d characters", maxChars, len(o.body()))
			}
		}
	}
}

// TestCapSkipsWhatDoesNotFit: an item that does not fit no longer ends
// the answer: a lower one that fits is still printed and Left counts
// only what was left out.
func TestCapSkipsWhatDoesNotFit(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("The nightly backup notes go on and on. ", 40)
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_a", Place: "repo", Body: "The nightly backup runs at 03:15.", At: now.AddDate(0, 0, -1)},
		{Table: "memories", ID: "mem_b", Place: "repo", Body: long, At: now.AddDate(0, 0, -2)},
		{Table: "memories", ID: "mem_c", Place: "repo", Body: "The nightly backup is checked on Tuesdays.", At: now.AddDate(0, 0, -3)},
	}}
	o := build(parseQuestion("when does the nightly backup run", now), d, 0, 400)
	var got []string
	for _, r := range o.Rows {
		got = append(got, r.ID)
	}
	if strings.Join(got, ",") != "mem_a,mem_c" || o.Left != 1 {
		t.Errorf("rows %v left %d, want mem_a,mem_c and 1 left out\n%s", got, o.Left, o.Text())
	}
}

// TestExcerpt: the excerpt is the section that holds the question's
// words, a contiguous part of the text within room, and the start of the
// text when nothing matches.
func TestExcerpt(t *testing.T) {
	t.Parallel()
	doc := longDoc()
	words := []string{"nightly", "backup", "billing", "database", "run"}
	ex, cut := excerpt(doc, words, 600)
	if !cut || !strings.Contains(ex, "## Operations") || !strings.Contains(ex, "vault-seven") || len(ex) > 600 || !strings.Contains(doc, ex) {
		t.Errorf("excerpt %q (cut %v)", ex, cut)
	}
	ex, _ = excerpt(doc, []string{"zebra"}, 300)
	if !strings.HasPrefix(doc, ex) || len(ex) > 300 {
		t.Errorf("no match: excerpt %q is not the start", ex)
	}
	one := strings.Repeat("word ", 100) + "the billing backup runs nightly. " + strings.Repeat("more ", 100)
	ex, _ = excerpt(one, words, 120)
	if !strings.Contains(ex, "billing backup runs nightly") || len(ex) > 120 {
		t.Errorf("one paragraph: excerpt %q", ex)
	}
	if ex, cut := excerpt("short", words, 100); cut || ex != "short" {
		t.Errorf("a text that fits is cut: %q", ex)
	}
	// devco D-970: the matching sentence under its heading path, then the
	// opening; text left out is marked
	if body := cutText(doc, words, 600); !strings.Contains(body, "## Operations\nThe nightly backup") || !strings.HasPrefix(body, "# Platform design notes\n") || !strings.Contains(body, "\n"+elision+"\n") || !strings.HasSuffix(body, "\n"+elision) || len(body) > 600 {
		t.Errorf("cut body lost its heading path or elision marks: %q", body)
	}
	// a row with no heading is cut by paragraph, marked where text was left out
	plain := strings.ReplaceAll(strings.ReplaceAll(doc, "# ", ""), "#", "")
	if body, cut := cutBody(plain, words, 600); !cut || !strings.Contains(body, "vault-seven") || !strings.HasSuffix(body, "\n"+elision) || strings.Contains(body, sectionMark) {
		t.Errorf("a row with no heading: %q", body)
	}
}

// sectionNote is a ~5,000-character design note shaped like the long
// notes of the follow-up of issue #86 (devco D-888): every section
// repeats some of the question's words, the Overview has a paragraph
// holding as many of them as the paragraph with the answer, and only the
// Rollout plan section (by its heading and its text) holds all of them.
func sectionNote() string {
	filler := func(topic string) string {
		return strings.Repeat("The "+topic+" notes cover the rollout in plain terms; the team reviewed them in a sprint, kept the layout and wrote every follow-up down in the tracker. ", 4)
	}
	var b strings.Builder
	b.WriteString("# Orbit design notes\n\n## Overview\n\n")
	b.WriteString("Phase two of the rollout is the biggest step of the year, and the whole team takes part in it.\n\n")
	b.WriteString(filler("overview") + "\n\n" + filler("scope") + "\n\n")
	for _, topic := range []string{"Goals", "Architecture", "Budget"} {
		b.WriteString("## " + topic + "\n\n" + filler(strings.ToLower(topic)) + "\n\n" + filler(strings.ToLower(topic)+" review") + "\n\n")
	}
	b.WriteString("## Rollout plan\n\n" + filler("pilot") + "\n\n")
	b.WriteString("Phase two starts on 2026-06-05, once the pilot has run for a month.\n\n")
	b.WriteString("## Risks\n\n" + filler("risk") + "\n\n" + filler("mitigation") + "\n")
	return b.String()
}

// TestCutBodyBestSections (devco D-888, follow-up of public issue #86):
// a long markdown row is cut to its best sections by heading, best first,
// each after a § line with its heading path: the section whose heading
// and text hold the question's words that the other sections do not,
// not the first paragraph holding as many of them (the old cut printed
// the Overview).
func TestCutBodyBestSections(t *testing.T) {
	t.Parallel()
	doc := sectionNote()
	words := cutWords([]string{"phase", "two", "orbit", "rollout", "start"}, nil)
	for _, room := range []int{2000, 900, minCut} {
		body, cut := cutBody(doc, words, room)
		first, _, _ := strings.Cut(body, "\n")
		if !cut || first != sectionMark+"Orbit design notes > Rollout plan" || !strings.Contains(body, "starts on 2026-06-05") {
			t.Errorf("room %d: the best section is not printed first:\n%s", room, body)
		}
		if len(body) > room {
			t.Errorf("room %d: %d characters", room, len(body))
		}
		if strings.Contains(body, "## ") {
			t.Errorf("room %d: a heading line is printed, not the § path:\n%s", room, body)
		}
	}
	// the next best section follows while it fits (the Overview holds
	// phase and two too); a section holding only words every section
	// holds (rollout, orbit in the path) is not printed
	body, _ := cutBody(doc, words, 2400)
	if !strings.Contains(body, "\n\n"+sectionMark+"Orbit design notes > Overview\n") || strings.Count(body, sectionMark) != 2 {
		t.Errorf("the second best section does not follow alone:\n%s", body)
	}
	if body, _ := cutBody(doc, words, 2000); strings.Count(body, sectionMark) != 1 {
		t.Errorf("a section holding only common words is printed:\n%s", body)
	}
	// through the answer: the best row is printed as its best sections
	d := data{rows: []textRow{{Table: "memories", ID: "mem_note", Place: "me/notes", Body: doc, At: now.AddDate(0, 0, -1)}}}
	o := build(parseQuestion("when does phase two of the orbit rollout start", now), d, 0, DefaultMaxChars)
	text := o.Text()
	if len(o.Rows) != 1 || o.Rows[0].FullChars != len(doc) || !strings.Contains(text, "   "+sectionMark+"Orbit design notes > Rollout plan\n") || !strings.Contains(text, "starts on 2026-06-05") || len(o.body()) > DefaultMaxChars {
		t.Errorf("the answer does not print the best section:\n%s", text)
	}
}

// TestSections: headings split a note into sections with their heading
// path (ATX and setext), never inside a code fence; a heading with no
// text of its own joins the section after it; text before the first
// heading is a section of its own; a text with no heading has none.
func TestSections(t *testing.T) {
	t.Parallel()
	doc := "intro line\n\n# Title\n\n## Part one ##\n\none text\n\n```sh\n# not a heading\n```\n\nPart two\n--------\n\ntwo text\n\n### Deep\n\ndeep text\n\n## Part three\n\nthree text\n"
	var got []string
	for _, s := range sections(doc) {
		got = append(got, s.path+"="+strings.Join(strings.Fields(s.text), " "))
	}
	want := []string{
		topPath + "=intro line",
		"Title > Part one=one text ```sh # not a heading ```",
		"Title > Part two=two text",
		"Title > Part two > Deep=deep text",
		"Title > Part three=three text",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("sections:\n got %q\nwant %q", got, want)
	}
	if s := sections("no heading here\n\n- a list\n---\n"); s != nil {
		t.Errorf("a text with no heading has sections: %q", s)
	}
}
