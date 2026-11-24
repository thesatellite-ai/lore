package docnotes

import (
	"slices"
	"strings"
	"testing"
)

// devco:D-940, D-962: a doc splits into one piece per section titled
// with its heading path; code, tables, HTML and front matter are left
// out, and a paragraph stating a choice with its reason is a decision of
// its own (cut out of its section's fact).
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
		kind  Kind
		line  int
		title string
		text  string
		refs  []string
	}
	wants := []want{
		{Fact, 8, "Billing › How it works", "Billing › How it works\n\n" +
			"The worker pulls jobs from the queue every five seconds and parks a job after three tries (see internal/queue/worker.go:12).\n\n" +
			"- Invoices are rendered as PDF and kept ninety days.\n- ok\n  1. Nested step one is long enough to count here.", []string{"internal/queue/worker.go:12"}},
		{Decision, 29, "We chose Postgres advisory locks over Redis locks because the service already…",
			"Billing › How it works › Decisions\n\nWe chose Postgres advisory locks over Redis locks because the service already depends on Postgres.", nil},
		{Fact, 31, "Billing › How it works › Decisions", "Billing › How it works › Decisions\n\n" +
			"We use Postgres for storage of every invoice the service renders. The meeting starts at 10:30 and the API listens on localhost:8080 today.", nil},
	}
	if len(got) != len(wants) {
		for _, p := range got {
			t.Logf("%d %s %q", p.Line, p.Kind, p.Text)
		}
		t.Fatalf("Split gave %d pieces, want %d", len(got), len(wants))
	}
	for i, w := range wants {
		p := got[i]
		if p.Kind != w.kind || p.Line != w.line || p.Title != w.title || p.Text != w.text || !slices.Equal(p.CodeRefs, w.refs) {
			t.Errorf("piece %d = {%s %d %q %q %v},\nwant {%s %d %q %q %v}", i, p.Kind, p.Line, p.Title, p.Text, p.CodeRefs, w.kind, w.line, w.title, w.text, w.refs)
		}
	}
	// a choice with no reason stays a fact: the reason is what a decision records
	if p := Split("We picked the blue theme for the dashboard last spring."); len(p) != 1 || p[0].Kind != Fact {
		t.Errorf("a choice without a reason = %+v, want one fact", p)
	}
}

// devco:D-962 (bug hunt H-221, issue #162): a technical doc drafts one
// note per section, never one per paragraph or list item. A list keeps its
// lead-in and nested items (item 1 too), a section of labelled Decision. /
// Considered. / Why. paragraphs is ONE decision titled with its heading
// (every alternative kept), and the table of contents, the license and
// the contributing line are left out. Before the fix this doc gave 12
// pieces: no heading on any, list items cut from their lead-in, D1 as
// four facts titled "Decision", "Considered", "Considered", "Why".
func TestSplitDraftsOneNotePerSection(t *testing.T) {
	doc := strings.Join([]string{
		"# widget module: technical notes", "",
		"## Contents", "", "- [Overview](#overview)", "- [Decisions](#decisions)", "- [Caching](#caching)", "- [License](#license)", "",
		"## Overview", "",
		"The widget module imports widgets from upstream feeds. It runs inside the worker.", "",
		"The import has three phases:", "",
		"1. Fetch the bulk file.", "2. Parse it in batches of 500.", "   - A bad row is skipped, not fatal.", "   - Skipped rows are counted in the step output.",
		"3. Write the rows through the single writer.", "",
		"See [the feed spec](feeds.md).", "",
		"## Decisions", "",
		"### D1. One writer for widget rows", "",
		"Decision. Every widget write goes through widget.Save.", "", "Considered. Direct SQL from the importer.", "",
		"Considered. A trigger in the database.", "", "Why. Checks and history cannot be skipped.", "",
		"### D2. Batch size", "", "**Decision:** 500 rows per batch.", "", "Considered. 5000.", "",
		"## Caching", "", "Things that invalidate it:", "", "- an import,", "- a manual purge.", "",
		"## License", "", "MIT. See LICENSE.", "", "Contributions welcome; open a pull request.",
	}, "\n")
	got, dropped := SplitCounted(doc)
	// the contents and license sections, and the "See [the feed spec]" pointer
	if dropped != (Dropped{Boilerplate: 2, LinkOnly: 1}) {
		t.Errorf("dropped = %+v, want 2 boilerplate sections and 1 link-only block", dropped)
	}
	if tiny, d := SplitCounted("# A\n\n## B\n\nok then.\n\n- [x](y)\n"); len(tiny) != 0 || d != (Dropped{LinkOnly: 1, TooShort: 1}) {
		t.Errorf("a two-word section = %+v %+v, want nothing and one too short", tiny, d)
	}
	type want struct {
		kind  Kind
		line  int
		title string
		text  string
	}
	wants := []want{
		{Fact, 12, "widget module: technical notes › Overview", "widget module: technical notes › Overview\n\n" +
			"The widget module imports widgets from upstream feeds. It runs inside the worker.\n\nThe import has three phases:\n\n" +
			"1. Fetch the bulk file.\n2. Parse it in batches of 500.\n   - A bad row is skipped, not fatal.\n   - Skipped rows are counted in the step output.\n" +
			"3. Write the rows through the single writer."},
		{Decision, 28, "One writer for widget rows", "widget module: technical notes › Decisions › D1. One writer for widget rows\n\n" +
			"Decision. Every widget write goes through widget.Save.\n\nConsidered. Direct SQL from the importer.\n\n" +
			"Considered. A trigger in the database.\n\nWhy. Checks and history cannot be skipped."},
		{Decision, 38, "Batch size", "widget module: technical notes › Decisions › D2. Batch size\n\n**Decision:** 500 rows per batch.\n\nConsidered. 5000."},
		{Fact, 44, "widget module: technical notes › Caching", "widget module: technical notes › Caching\n\nThings that invalidate it:\n\n- an import,\n- a manual purge."},
	}
	if len(got) != len(wants) {
		for _, p := range got {
			t.Logf("%d %s %q %q", p.Line, p.Kind, p.Title, p.Text)
		}
		t.Fatalf("Split gave %d pieces, want %d (one per section)", len(got), len(wants))
	}
	for i, w := range wants {
		p := got[i]
		if p.Kind != w.kind || p.Line != w.line || p.Title != w.title || p.Text != w.text {
			t.Errorf("piece %d = {%s %d %q %q},\nwant {%s %d %q %q}", i, p.Kind, p.Line, p.Title, p.Text, w.kind, w.line, w.title, w.text)
		}
	}
}

// devco:D-962: a section longer than MaxPieceRunes is cut into parts at
// paragraph boundaries, never between a lead-in and its list; dropping a
// block (the repeat check) rebuilds the text and keeps the heading.
func TestSplitCutsLongSectionsAndRebuilds(t *testing.T) {
	para := strings.TrimSpace(strings.Repeat("The queue worker retries a failed job with a growing delay. ", 20))
	doc := "# Queue\n\n" + para + "\n\n" + para + " Again.\n\nIt has two modes:\n\n- fast mode,\n- safe mode.\n"
	got := Split(doc)
	if len(got) != 2 || got[0].Title != "Queue (part 1)" || got[1].Title != "Queue (part 2)" {
		for _, p := range got {
			t.Logf("%q %d runes", p.Title, len([]rune(p.Text)))
		}
		t.Fatalf("a long section gave %d pieces, want 2 titled by part", len(got))
	}
	if b := got[1].Blocks; len(b) != 3 || !strings.HasSuffix(got[1].Text, "It has two modes:\n\n- fast mode,\n- safe mode.") {
		t.Fatalf("part 2 = %q, want the second paragraph with the lead-in and its list", got[1].Text)
	}
	p, ok := got[1].WithBlocks(got[1].Blocks[1:])
	if !ok || p.Text != "Queue\n\nIt has two modes:\n\n- fast mode,\n- safe mode." || p.Line != got[1].Blocks[1].Line || p.Title != got[1].Title {
		t.Fatalf("WithBlocks = %+v %v", p, ok)
	}
	if _, ok := got[1].WithBlocks(nil); ok {
		t.Fatal("WithBlocks of nothing made a piece")
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
