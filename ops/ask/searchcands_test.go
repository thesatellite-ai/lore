package ask

// searchcands_test.go — bug hunt H-270 (devco D-1008): ask's candidate
// list is lore search's next rows in search's order (a row it leaves out
// says why), and a candidate's line is the sentence that best matches the
// question, its rare words weighing most, after its heading.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/querywords"
)

// folderFixture holds two folders answering the same question with the
// same words: eight untitled notes in the repo's linked folder (kb/app),
// whose first 80 bytes (search's label for a note without a title) hold
// none of the asked words, and four notes in another folder (kb/ops)
// whose titles name them. ask ranks its full rows with the linked folder first; search
// ranks the titled notes first.
func folderFixture() data {
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	var d data
	for i := range 8 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_app%02d", i), Place: "kb/app",
			Body: fmt.Sprintf("App note %d, kept by the billing team for the quarterly close and the yearly audit of each ledger.\nThe invoice export retries a failed page %d times before it stops.", i, i+2), At: at.Add(-time.Duration(i) * time.Hour)})
	}
	for i := range 4 {
		d.rows = append(d.rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_ops%02d", i), Place: "kb/ops", Title: "Invoice export retries",
			Body: fmt.Sprintf("Ops note %d: the invoice export retries a failed page with a backoff of %d s.", i, i+1), At: at.Add(-time.Duration(i) * time.Hour)})
	}
	return d
}

func TestCandidatesAreSearchNextRows(t *testing.T) {
	t.Parallel()
	q := parseQuestion("how often does the invoice export retry a failed page", now)
	q.home = "kb/app"
	o := build(q, folderFixture(), 1, 1500)
	text := o.Text()
	if len(o.Candidates) == 0 {
		t.Fatalf("no candidates:\n%s", text)
	}
	// search's first hits are the notes whose titles name the words: the
	// list starts with them, whatever folder ask printed in full
	printed := map[string]bool{}
	for _, r := range o.Ranked {
		printed[r.ID] = true
	}
	var want []string
	for i := range 4 {
		if id := fmt.Sprintf("mem_ops%02d", i); !printed[id] {
			want = append(want, id)
		}
	}
	var got []string
	for _, c := range o.Candidates[:min(len(want), len(o.Candidates))] {
		got = append(got, c.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the list opens with %v, want search's next rows %v:\n%s", got, want, text)
	}
}

func TestCandidatesCarrySearchRank(t *testing.T) {
	t.Parallel()
	q := parseQuestion("how often does the invoice export retry a failed page", now)
	q.home = "kb/app"
	d := folderFixture()
	o := build(q, d, 1, 1500)
	order := plainOrder(q, d.rows, nil)
	last := 0
	for _, c := range o.Candidates {
		if c.Kind != KindRow {
			continue
		}
		if c.Rank <= last || order[c.Rank-1] != c.ID {
			t.Errorf("%s has rank %d, search's order is %v", c.ID, c.Rank, order)
		}
		last = c.Rank
	}
	// every row search ranks before the list's last one is printed in
	// full, listed or dropped with a reason
	shown := map[string]bool{}
	for _, r := range o.Ranked {
		shown[r.ID] = true
	}
	for _, c := range o.Candidates {
		shown[c.ID] = true
	}
	for _, x := range o.Dropped {
		shown[x.ID] = true
	}
	for _, id := range order[:last] {
		if !shown[id] {
			t.Errorf("search's %s (before the list's last row) is missing without a reason:\n%s", id, o.Text())
		}
	}
}

func TestCandidatesDropSameNote(t *testing.T) {
	t.Parallel()
	d := folderFixture()
	// the same note stored twice (a copy in another folder)
	dup := d.rows[8]
	dup.ID, dup.Place = "mem_copy00", "kb/copy"
	d.rows = append(d.rows, dup)
	q := parseQuestion("how often does the invoice export retry a failed page", now)
	o := build(q, d, 1, 1500)
	text := o.Text()
	listed := map[string]bool{}
	for _, c := range o.Candidates {
		listed[c.ID] = true
	}
	if listed["mem_ops00"] && listed["mem_copy00"] {
		t.Fatalf("the same note is listed twice:\n%s", text)
	}
	var reason string
	for _, x := range o.Dropped {
		if x.ID == "mem_copy00" || x.ID == "mem_ops00" {
			reason = x.Reason
		}
	}
	if !strings.HasPrefix(reason, "same text as mem_") {
		t.Errorf("the second copy is dropped without its reason (%q): %+v\n%s", reason, o.Dropped, text)
	}
	if !strings.Contains(text, DroppedHeading) || !strings.Contains(text, reason) {
		t.Errorf("the text answer does not say why a row is left out:\n%s", text)
	}
}

func TestCandidateLineIsMatchingSentence(t *testing.T) {
	t.Parallel()
	q := parseQuestion("how does the sync job reissue the workspace token", now)
	// sync, job and workspace are common in the store, reissue is rare
	st := &subjectTerms{weight: map[string]float64{"sync": 0.2, "job": 0.2, "workspace": 0.2, "token": 0.4, "reissue": 3}, rare: map[string]bool{"reissue": true}}
	r := Row{ID: "anr_1", Table: "architecture_notes", Title: "Workspace tokens", subject: st,
		Body: "# Workspace tokens\n\nThe sync job runs every hour for each workspace and logs what the sync job did.\n\n## Lost installs\n\nA lost install makes the app reissue the token: ReissueWorkspaceToken checks the owner first.\n"}
	o := &Out{q: q}
	c := o.rowCandidate(&r)
	if !strings.Contains(c.Line, "reissue the token") {
		t.Errorf("the line is %q, want the sentence holding the rare word", c.Line)
	}
	if !strings.HasPrefix(c.Line, "Lost installs › ") {
		t.Errorf("the line %q does not lead with its heading", c.Line)
	}
}

func TestPlainOrderReadsSyntaxQuestionAsWords(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{
		// three words of the question, all in its title
		{Table: "decisions", ID: "dec_one", Place: "kb/app", Title: "Master mapping table", Body: "Colours of the screens.", At: at},
		// most of the question's words, none in its title
		{Table: "decisions", ID: "dec_many", Place: "kb/app", Title: "Why it is separate", Body: "The master mapping has its own table (acme_masters) so every order reads one copy; an edit to it happens once.", At: at},
	}}
	// a parenthesis is FTS5 syntax; the question mark makes FTS5 refuse
	// it, and search reads the plain words
	q := parseQuestion("Why is the master mapping its own table (acme_masters) rather than part of each order, and what happens when someone edits it?", now)
	if got := plainOrder(q, d.rows, nil); len(got) < 2 || got[0] != "dec_many" {
		t.Errorf("search's order %v, want the row holding most words first", got)
	}
	if !refusedByFTS(q.text) || refusedByFTS(`deploy -friday "exact phrase, kept"`) {
		t.Errorf("refusedByFTS: a question with ? is refused, a query FTS5 reads is not")
	}
}

func TestCoverOfIsCoverage(t *testing.T) {
	t.Parallel()
	r := &textRow{Title: "Label printers", Body: "The labels jam when the printer's queue is deployed; invoi* and acme_widgets stay."}
	for _, terms := range [][]string{
		{"label", "printer", "queue", "deploy", "jams"},
		{"Label", "missing", "jammed"},
		{"invoi*", "label"},
		{"acme_widgets", "queue"},
	} {
		for range 2 { // the second call reads the kept bases
			if got, want := coverOf(r, terms, coverBases(terms)), querywords.Coverage(docText(r), terms); got != want {
				t.Errorf("coverOf(%q) = %d, Coverage = %d", terms, got, want)
			}
		}
	}
}
