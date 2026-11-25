package ask

// forms_test.go — devco D-976 (bug hunt H-235, Sync benchmark set 6 item
// 5): a question word written in another form than the row writes it
// (validate / validation, jobs / job, enqueuing / enqueue, encrypted /
// re-encrypts) did not match in lore ask, though lore search stems both
// and ranks the row first; a row about something else that wrote the
// question's other words in the question's own form ranked first, and
// sibling questions on one subject got different rows.

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestHasWordStemForms: hasWord joins the forms lore search's stemmer
// joins, and still joins the prefix forms it joined before.
func TestHasWordStemForms(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text, word string
		want       bool
	}{
		{"each validation rule holds a field", "validate", true},
		{"the catalog validates a product", "validation", true},
		{"how are catalog validations run", "validate", true},
		{"the enqueue gate refuses a job", "enqueuing", true},
		{"the enqueue gate refuses a job", "jobs", true},
		{"the queue keeps all jobs", "job", true},
		{"worker vault-rekey re-encrypts every credential", "encrypted", true},
		{"the importer writes rows", "writing", true},
		{"two budgets for orbit", "budget", true}, // a prefix form, as before
		{"the appointments of alice", "appointment", true},
		{"one writer per catalog table", "validate", false},
		{"the gateway forwards calls", "gates", false},
	} {
		if got := hasWord(tokensOf(c.text), c.word); got != c.want {
			t.Errorf("hasWord(%q, %q) = %v, want %v", c.text, c.word, got, c.want)
		}
	}
}

// TestStemKeysAreNoWords: the stem keys a word set holds are never read
// as words: not by contentOf (restates), not as a typo of a question
// word (heldByAny), and a section heading keeps no stem of a word the
// row's title holds.
func TestStemKeysAreNoWords(t *testing.T) {
	t.Parallel()
	r := textRow{ID: "mem_x", Body: "the queue keeps all jobs and validations"}
	if slices.ContainsFunc(contentOf(&r), isStemKey) {
		t.Errorf("contentOf holds a stem key: %q", contentOf(&r))
	}
	secs := splitSections("Validations", "## Validations\n\nOne.\n\n## Jobs\n\nTwo.\n\n")
	if len(secs) != 2 || hasWord(secs[0].head, "validate") || !hasWord(secs[1].head, "job") {
		t.Errorf("section heads %v: the title word's stem stayed in a heading, or a heading lost its own", secs)
	}
}

// formsBackground is a store where the off-topic rows write the
// questions' other words in the questions' own form, and filler rows so
// a word's spread reads like a real store's.
func formsBackground(at time.Time) []textRow {
	mem := func(id, body string, h int) textRow {
		return textRow{Table: "memories", ID: id, Place: "kb/app", Body: body, At: at.Add(time.Duration(h) * time.Hour)}
	}
	rows := []textRow{
		{Table: "rules", ID: "rul_writer", Place: "kb/app", Body: "One writer per catalog table: only the catalog worker writes product fields and rules; other services read them.", At: at.Add(5 * time.Hour)},
		{Table: "architecture_notes", ID: "anr_attr", Place: "kb/app", Title: "Attribute value writer", Body: "The attribute value writer runs sync jobs per product; a gate skips the jobs of a product whose values did not change.", At: at.Add(5 * time.Hour), FactState: "live", Verified: "2026-11-20"},
		{Table: "hotfixes", ID: "hfx_restart", Place: "kb/app", Title: "Worker restart keeps stored credentials", Body: "The worker restarts when a job fails; the stored credentials are kept, so the next worker run goes on.", At: at.Add(5 * time.Hour)},
		mem("mem_logs", "The catalog worker logs each product it reads.", 1),
		mem("mem_print", "A sync job prints the product it wrote.", 1),
		mem("mem_field", "Each product field is stored with its value.", 1),
		mem("mem_table", "The worker reads each catalog table.", 1),
		mem("mem_week", "The job queue keeps each job for a week.", 1),
		mem("mem_creds", "Stored credentials are read by the worker.", 1),
		mem("mem_price", "A product has a title, a price and its fields.", 1),
		mem("mem_hour", "The catalog sync runs every hour.", 1),
	}
	for i := 1; i <= 12; i++ {
		rows = append(rows, mem(fmt.Sprintf("mem_dash%d", i), fmt.Sprintf("Note %d: the dashboard lists each workspace with its plan, owner and the last deploy.", i), 0))
	}
	return append(rows,
		mem("mem_valid", "Catalog validation: each validation rule holds a field, an operator and a severity; an error severity blocks the publish, a warning only shows.", 0),
		mem("mem_gate", "The enqueue gate refuses a job while the same job key is still queued and returns the id of the first job.", 0),
		textRow{Table: "hotfixes", ID: "hfx_rekey", Place: "kb/app", Title: "vault-rekey", Body: "worker vault-rekey re-encrypts every stored credential under the newest key version; it refuses to run without --confirm and while another rekey holds the lock.", At: at},
	)
}

// TestFormsAnswerFirst: sibling questions on one subject, worded with
// other forms of its words, all get the row that answers first; so do
// the off-topic-first shapes (S6-05 enqueue gate, S6-34 vault-rekey).
func TestFormsAnswerFirst(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	rows := formsBackground(at)
	for _, c := range []struct{ name, q, want string }{
		{"S6-27", "what does a catalog validation rule hold", "mem_valid"},
		{"S6-28", "which product fields and rules does the catalog validate", "mem_valid"},
		{"S6-29", "how are catalog validations of a field run", "mem_valid"},
		{"S6-05", "what does the gate check before enqueuing jobs", "mem_gate"},
		{"S6-34", "which worker command encrypted the stored credentials again", "hfx_rekey"},
	} {
		o := build(parseQuestion(c.q, now), data{rows: slices.Clone(rows)}, 0, 0)
		if ids := rowIDs(o.Rows); len(ids) == 0 || ids[0] != c.want {
			t.Errorf("%s %q: rows %v, want %s first:\n%s", c.name, c.q, ids, c.want, firstLines(o.Text(), 20))
		}
	}
}
