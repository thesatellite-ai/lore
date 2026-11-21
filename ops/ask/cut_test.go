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
	body, _ := cutBody(doc, words, 600)
	if !strings.HasPrefix(body, elision+"\n") || !strings.HasSuffix(body, "\n"+elision) {
		t.Errorf("cut body has no elision marks: %q", body)
	}
}
