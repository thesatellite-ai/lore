package ask

import (
	"fmt"
	"strings"
	"testing"
)

// midDoc is a markdown note between longRow and DefaultMaxChars long: it
// fits under the cap whole, and only its Operations section answers the
// backup question.
func midDoc() string {
	var b strings.Builder
	b.WriteString("# Vela design notes\n\n")
	for _, topic := range []string{"Request routing", "Session handling", "Search indexing"} {
		fmt.Fprintf(&b, "## %s\n\n", topic)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(&b, "This part of the design covers %s in plain terms. The team reviewed the %s plan in sprint %d and agreed to keep the layout. ", topic, topic, i)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("## Operations\n\nThe nightly backup of the billing database runs at 03:15 UTC on host vault-seven.\n\n")
	b.WriteString("## Rate limiting\n\nThis part covers rate limiting in plain terms; the team agreed to revisit it later and keep the current limits for now.\n")
	return b.String()
}

// TestLongRowPrintsBestSections (devco D-892): a markdown row longer than
// longRow characters prints its best sections by heading, not whole, even
// when it fits under --max-chars, says how long it is whole and how to
// read it whole; a shorter row and a row without headings stay whole.
func TestLongRowPrintsBestSections(t *testing.T) {
	t.Parallel()
	doc := midDoc()
	if len(doc) <= longRow || len(doc) >= DefaultMaxChars {
		t.Fatalf("fixture is %d characters, want between %d and %d", len(doc), longRow, DefaultMaxChars)
	}
	plain := strings.Repeat("The billing database backup is described in the runbook. ", 30)
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_long", Place: "repo", Body: doc, At: now.AddDate(0, 0, -1)},
		{Table: "memories", ID: "mem_plain", Place: "repo", Body: plain, At: now.AddDate(0, 0, -2)},
	}}
	o := build(parseQuestion("when does the nightly backup of the billing database run", now), d, 0, 0)
	text := o.Text()
	if len(o.Rows) != 2 || o.Rows[0].ID != "mem_long" {
		t.Fatalf("rows %v\n%s", o.Rows, text)
	}
	long := o.Rows[0]
	if long.FullChars != len(doc) || len(long.Body) > longRoom {
		t.Errorf("long row: full_chars %d body %d, want %d and at most %d", long.FullChars, len(long.Body), len(doc), longRoom)
	}
	for _, want := range []string{"§ Vela design notes > Operations", "03:15 UTC on host vault-seven", "lore show --id mem_long prints it whole"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "Session handling in plain terms") {
		t.Errorf("a section that does not answer is printed:\n%s", text)
	}
	if p := o.Rows[1]; p.FullChars != 0 || p.Body != plain {
		t.Errorf("a long row without headings was cut: full_chars %d", p.FullChars)
	}
	short := midDoc()[:longRow-100]
	o = build(parseQuestion("when does the nightly backup of the billing database run", now), data{rows: []textRow{{Table: "memories", ID: "mem_short", Place: "repo", Body: short, At: now}}}, 0, 0)
	if len(o.Rows) == 1 && (o.Rows[0].FullChars != 0 || o.Rows[0].Body != short) {
		t.Errorf("a row under %d characters was cut: %+v", longRow, o.Rows[0])
	}
}

// TestCompactAnswer (devco D-892): a rows answer prints one line per row
// ("- <day> <folder>: <text>") with no per-row id, table or "best rows"
// heading; the folder is said once when every row shares it, every id is
// on the cited line in reading order, a resource under rows is one line,
// and --json keeps every field.
func TestCompactAnswer(t *testing.T) {
	t.Parallel()
	o, text := ask(t, "what is the status of lumen")
	if strings.Contains(text, "best rows") || strings.Contains(text, "memories mem_") || strings.Contains(text, "\n\n") {
		t.Errorf("not compact:\n%s", text)
	}
	if len(o.Rows) == 0 || len(o.Resources) == 0 {
		t.Fatalf("rows %v resources %v\n%s", o.Rows, o.Resources, text)
	}
	r := o.Rows[0]
	if !strings.HasPrefix(text, "now: "+oneLine(r)) && !strings.HasPrefix(text, "- "+r.At+" "+r.Place+": ") {
		t.Errorf("first line is not the best row:\n%s", text)
	}
	if !strings.Contains(text, "\n- 2026-05-01 me/work: Status update lumen: kickoff held with 10 people.  [history: ") {
		t.Errorf("an older row is not one line with its history:\n%s", text)
	}
	if !strings.Contains(text, "\nproject lumen: company acme-co, owner alice-ng\n") {
		t.Errorf("the project under the rows is not one line:\n%s", text)
	}
	if !strings.HasSuffix(text, "cited: "+strings.Join(o.Cited, " ")+"\n") || len(o.Cited) != len(o.Ranked) {
		t.Errorf("cited line does not name every id:\n%s", text)
	}
	if r.ID == "" || r.Table == "" || r.Body == "" {
		t.Errorf("--json row lost a field: %+v", r)
	}
}
