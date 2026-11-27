package ask

// setnine_test.go — bug hunt H-288 (devco D-1028, Sync benchmark set 9
// asks a-c): a row of at most shortRow characters is printed whole, never
// cut; a row writing the command the question quotes in backticks comes
// first; of rows holding as many of the question's words, the one
// holding its rarer word comes first; and search's own first row, when
// the answer does not print it, is the first candidate line, before any
// resource card.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// shortSectionsNote is a markdown note of about 2,100 characters: three
// sections, the asked rule in the last one.
func shortSectionsNote() string {
	pad := strings.Repeat("The widget importer reads each page of the feed and keeps a cursor per shop. ", 6)
	return "## Overview\n" + pad + "\n\n## Retries\n" + pad + "\n\n## Limits\n" + pad +
		"\nThe importer stops after five failed pages in a row and marks the run failed."
}

func TestShortRowPrintedWhole(t *testing.T) {
	t.Parallel()
	body := shortSectionsNote()
	if len(body) <= longRow || len(body) > shortRow {
		t.Fatalf("fixture is %d characters, want between %d and %d", len(body), longRow, shortRow)
	}
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_importer", Place: "kb/app", Title: "Widget importer limits", Body: body, At: at},
		{Table: "memories", ID: "mem_other", Place: "kb/app", Body: "The widget importer runs every night for each shop.", At: at},
	}}
	q := parseQuestion("when does the widget importer stop after failed pages", now)
	o := build(q, d, DefaultTop, DefaultMaxChars)
	text := o.Text()
	for _, r := range o.Rows {
		if r.ID != "mem_importer" {
			continue
		}
		if r.FullChars != 0 || r.Body != body {
			t.Fatalf("the %d-character note was cut (whole %d), want it whole:\n%s", len(body), r.FullChars, text)
		}
		if !strings.Contains(text, "marks the run failed") {
			t.Errorf("the asked rule is not on the page:\n%s", text)
		}
		return
	}
	t.Fatalf("the note is not printed:\n%s", text)
}

// bumpFixture: a long as-built note holding more of the question's
// words than the short memory naming the quoted command, plus filler
// rows that share the common words.
func bumpFixture() data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	note := "Acme monorepo layout as built. The widget monorepo holds every package; each task in the Taskfile changes one package. " +
		"A dependency release is raised by hand per package. Local builds leave files changed in the monorepo. " +
		strings.Repeat("Packages share one lock file and each task names its package. ", 20)
	d := data{rows: []textRow{
		{Table: "architecture_notes", ID: "anr_layout", Place: "kb/app", Title: "Acme monorepo layout as built", Body: note, FactState: "built", At: at},
		{Table: "memories", ID: "mem_bump", Place: "kb/app", Body: "`task deps-bump` raises every package of the widget monorepo to its newest minor release, then rewrites the lock file.", At: at},
	}}
	for i := range 6 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_fill%d", i), Place: "kb/app",
			Body: fmt.Sprintf("Note %d: the monorepo task list for package %d.", i, i), At: at})
	}
	return d
}

func TestCodeSpanRowFirst(t *testing.T) {
	t.Parallel()
	q := parseQuestion("What does `task deps-bump` change in the widget monorepo, and which files does it leave changed locally?", now)
	o := build(q, bumpFixture(), DefaultTop, DefaultMaxChars)
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_bump" {
		t.Fatalf("first row %v, want mem_bump (it writes the quoted command):\n%s", rowIDs(o.Rows), o.Text())
	}
}

func TestSpansOf(t *testing.T) {
	t.Parallel()
	got := spansOf("What do `task  Deps-Bump` and `ErrQuota` do?")
	if len(got) != 1 || got[0] != "task deps-bump" {
		t.Errorf("spansOf = %q, want [task deps-bump] (a one-word span is an identifier)", got)
	}
}

func TestRarerWordBreaksTie(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{
		// as many of the question's words each; the note is an as-built
		// one (ranked a little first among equals), the memory holds the
		// word no other row holds
		{Table: "architecture_notes", ID: "anr_sweep", Place: "kb/app", Title: "Acme invoice sweep as built",
			Body: "The sweep purges archived invoice drafts on refund.", FactState: "built", At: at},
		{Table: "memories", ID: "mem_weekly", Place: "kb/app",
			Body: "A weekly sweep purges the archived drafts of an invoice.", At: at},
	}}
	for i := range 6 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_fill%d", i), Place: "kb/app",
			Body: fmt.Sprintf("Note %d: on refund the invoice keeps its drafts for %d days.", i, i+1), At: at})
	}
	q := parseQuestion("which weekly sweep purges archived invoice drafts after a refund", now)
	o := build(q, d, DefaultTop, DefaultMaxChars)
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_weekly" {
		t.Fatalf("first row %v, want mem_weekly (it holds the rarer word):\n%s", rowIDs(o.Rows), o.Text())
	}
}

func TestSearchFirstLeadsCandidates(t *testing.T) {
	t.Parallel()
	d := bumpFixture()
	// a card the question only touches (acme, a part of its name) whose
	// purpose shares more of the question's words than any row
	d.resources = []resourceRow{{ID: "res_api", Place: "kb/app", Kind: "code_repo", Name: "acme-api",
		Fields: []fieldValue{{"purpose", "the widget monorepo task runner: what each task changes, which files a bump leaves changed locally"}}}}
	q := parseQuestion("What does `task deps-bump` change in the acme widget monorepo, and which files does it leave changed locally?", now)
	o := build(q, d, 1, DefaultMaxChars)
	first := plainOrder(q, d.rows, nil)[0]
	for _, r := range o.Rows {
		if r.ID == first {
			t.Fatalf("search's first row %s is printed, want the fixture to leave it to the list:\n%s", first, o.Text())
		}
	}
	at := slices.IndexFunc(o.Candidates, func(c Candidate) bool { return c.ID == first })
	if at < 0 {
		t.Fatalf("search's first row %s is not listed: %v\n%s", first, candIDs(o.Candidates), o.Text())
	}
	for _, c := range o.Candidates[:at] {
		if !c.named {
			t.Errorf("%s comes before search's first row %s, want only a card the question names: %v\n%s", c.ID, first, candIDs(o.Candidates), o.Text())
		}
	}
	if !slices.ContainsFunc(o.Candidates, func(c Candidate) bool { return c.ID == "res_api" }) {
		t.Errorf("the card is gone, want it after search's first row: %v", candIDs(o.Candidates))
	}
}

func candIDs(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// TestCutKeepsCandidateSentence: a long note cut by its best sections
// keeps the sentence its candidate line would show (keepline.go): two
// sections spread the question's words over several sentences and win
// the cut, while one sentence of a later section holds them all.
func TestCutKeepsCandidateSentence(t *testing.T) {
	t.Parallel()
	pad := strings.Repeat("Deploys go through the shared scripts and the admin screen lists them. ", 3)
	body := "## Lifecycle\nThe scheduler starts each worker at boot. A job waits in the queue. Each worker reads its config once. The run row is written at the end. " + pad +
		"\n\n## Workers\nThe scheduler keeps a worker per shop. A job is retried twice. A worker reads the shop list. The audit row is kept a week. " + pad +
		"\n\n## Request path\n" + pad + "A scheduler worker takes the job and reads the trigger row before it runs.\n\n## Operations\nThe scheduler pauses a worker when a job fails twice. " + pad
	want := "A scheduler worker takes the job and reads the trigger row before it runs."
	words := []string{"row", "scheduler", "worker", "read", "job"}
	if text, cut := cutBody(body, words, 450, nil); !cut || strings.Contains(text, want) {
		t.Fatalf("fixture: the plain cut already holds the sentence (cut %v):\n%s", cut, text)
	}
	text, cut := cutKeeping(body, "", words, words, 450, nil)
	if !cut || !strings.Contains(text, want) || len(text) > 450 {
		t.Fatalf("cut %v, %d bytes, want the candidate sentence kept within 450:\n%s", cut, len(text), text)
	}
	if !strings.Contains(text, sectionMark+"Request path\n"+elision+"\n"+want) {
		t.Errorf("the kept sentence is not under its section path:\n%s", text)
	}
}

// TestLongNoteExcerptKeepsCandidateSentence: the same shape in a note over
// shortRow characters, cut by ask itself (chunkRow).
func TestLongNoteExcerptKeepsCandidateSentence(t *testing.T) {
	t.Parallel()
	pad := strings.Repeat("Deploys go through the shared scripts and the admin screen lists them. ", 9)
	body := "## Lifecycle\nThe scheduler starts each worker at boot. A job waits in the queue. Each worker reads its config once. The run row is written at the end. " + pad +
		"\n\n## Workers\nThe scheduler keeps a worker per shop. A job is retried twice. A worker reads the shop list. The audit row is kept a week. " + pad +
		"\n\n## Request path\n" + pad + "A scheduler worker takes the job and reads the trigger row before it runs.\n\n## Operations\nThe scheduler pauses a worker when a job fails twice. " + pad
	want := "A scheduler worker takes the job and reads the trigger row before it runs."
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{{Table: "architecture_notes", ID: "anr_sched", Place: "kb/app", Title: "Scheduler as built", Body: body, At: at}}}
	o := build(parseQuestion("what row does a scheduler worker read when it picks up a job", now), d, DefaultTop, DefaultMaxChars)
	if len(o.Rows) != 1 || o.Rows[0].FullChars == 0 {
		t.Fatalf("want the %d-character note cut:\n%s", len(body), o.Text())
	}
	if !strings.Contains(o.Text(), want) {
		t.Errorf("the excerpt lost the sentence its candidate line shows:\n%s", o.Text())
	}
}
