package docnotes

import (
	"slices"
	"strings"
	"testing"
)

// devco:D-940: a doc splits into one piece per paragraph or bullet, with
// code, tables, HTML, front matter and one-word bullets left out, the
// heading path kept, and a stated choice with its reason a decision.
func TestSplitDraftsFactsAndDecisions(t *testing.T) {
	doc := strings.Join([]string{
		"---",
		"title: ignored front matter with enough words here",
		"---",
		"# Billing",
		"",
		"## How it works",
		"",
		"The worker pulls jobs from the queue every five seconds",
		"and parks a job after three tries (see internal/queue/worker.go:12).",
		"",
		"- Invoices are rendered as PDF and kept ninety days.",
		"- ok",
		"  1. Nested step one is long enough to count here.",
		"",
		"```sh",
		"go run ./cmd/billing serve --port 8080 right now",
		"```",
		"",
		"| col a | col b | col c | col d | col e |",
		"|---|---|---|---|---|",
		"",
		"<!-- a comment that is long enough to count as words",
		"spanning two lines -->",
		"",
		"<div align=\"center\">a block of html that has enough words</div>",
		"",
		"### Decisions",
		"",
		"We chose Postgres advisory locks over Redis locks because the service already depends on Postgres.",
		"",
		"We use Postgres for storage of every invoice the service renders.",
		"The meeting starts at 10:30 and the API listens on localhost:8080 today.",
	}, "\n")
	got := Split(doc)
	type want struct {
		kind    Kind
		line    int
		section string
		start   string
		refs    []string
	}
	wants := []want{
		{Fact, 8, "Billing > How it works", "The worker pulls jobs from the queue every five seconds and parks", []string{"internal/queue/worker.go:12"}},
		{Fact, 11, "Billing > How it works", "Invoices are rendered as PDF", nil},
		{Fact, 13, "Billing > How it works", "Nested step one", nil},
		{Decision, 29, "Billing > How it works > Decisions", "We chose Postgres advisory locks", nil},
		{Fact, 31, "Billing > How it works > Decisions", "We use Postgres for storage of every invoice the service renders. The meeting starts", nil},
	}
	if len(got) != len(wants) {
		for _, p := range got {
			t.Logf("%d %s %q", p.Line, p.Kind, p.Text)
		}
		t.Fatalf("Split gave %d pieces, want %d", len(got), len(wants))
	}
	for i, w := range wants {
		p := got[i]
		if p.Kind != w.kind || p.Line != w.line || p.Section != w.section || !strings.HasPrefix(p.Text, w.start) || !slices.Equal(p.CodeRefs, w.refs) {
			t.Errorf("piece %d = {%s %d %q %q %v}, want {%s %d %q %q… %v}", i, p.Kind, p.Line, p.Section, p.Text, p.CodeRefs, w.kind, w.line, w.section, w.start, w.refs)
		}
	}
	if d := got[3]; d.Title != "We chose Postgres advisory locks over Redis locks because the service already…" {
		t.Errorf("decision title = %q", d.Title)
	}
	if got[0].Title != "" {
		t.Errorf("a fact has no title, got %q", got[0].Title)
	}
	// a choice with no reason stays a fact: the reason is what a decision records
	if p := Split("We picked the blue theme for the dashboard last spring."); len(p) != 1 || p[0].Kind != Fact {
		t.Errorf("a choice without a reason = %+v, want one fact", p)
	}
}

// devco:D-940: the same text folds to the same key whatever its spacing,
// case or punctuation, so a re-import finds what it wrote.
func TestNormFoldsSpacingCaseAndMarks(t *testing.T) {
	a, b := Norm("Invoices are **rendered**  as PDF."), Norm("invoices are rendered as pdf")
	if a != b || a == "" {
		t.Fatalf("Norm %q != %q", a, b)
	}
}
