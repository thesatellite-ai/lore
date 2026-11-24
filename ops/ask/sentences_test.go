package ask

import (
	"strings"
	"testing"
)

// stockWritePath is the "Write path" section of an S5-26-shaped as-built
// note (devco D-970, bug hunt H-229): three paragraphs repeat the
// question's common words (stock, sync, warehouse) and the one sentence
// naming the asked function's only caller sits in the last paragraph,
// past the room the paragraph cut grew to.
const stockWritePath = `The warehouse feed lands in the stock_inbox table; each row holds the warehouse id, the product, the new stock and the warehouse time. The stock sync worker reads the inbox in batches of 200 rows and writes the stock table per workspace, warehouse after warehouse, so one slow warehouse never holds the others.

Each stock sync of a warehouse runs in one transaction per batch, so a crash leaves the stock of that warehouse as the last batch wrote it, and the next sync of the warehouse starts from the inbox rows it did not mark done. The sync worker marks inbox rows done in the same transaction as the stock write.

Warehouse rows that fail to parse stay in the inbox with an error, and the stock sync worker retries them on the next sync of that warehouse. The stock table keeps the warehouse time of the last write, so an older warehouse row never overwrites newer stock. Each sync writes one audit row per warehouse with the number of stock rows it changed.

Batches are claimed with a lease of two minutes. A lease that runs out is taken over by the other replica. The only caller of applyLevelDelta is the nightly reconcile job in jobs/reconcile.go; the five-minute path never calls it. Leases are kept in the lease table, one row per claim, and swept hourly.`

const callerSentence = "The only caller of applyLevelDelta is the nightly reconcile job in jobs/reconcile.go; the five-minute path never calls it."

// TestCutTextKeepsMatchingSentences (devco D-970, bug hunt H-229, Sync
// benchmark set 5 ask 3): a row cut to fit keeps the sentence that names
// the identifier the question asks about, over paragraphs that only
// repeat its common words; every kind ask cuts goes through cutText.
func TestCutTextKeepsMatchingSentences(t *testing.T) {
	t.Parallel()
	words := []string{"calls", "applyleveldelta", "warehouse", "stock", "sync"}
	out := cutText(stockWritePath, words, 1000)
	if !strings.Contains(out, callerSentence) || len(out) > 1000 {
		t.Fatalf("the cut dropped the sentence naming the asked function (%d bytes):\n%s", len(out), out)
	}
	if !strings.HasSuffix(out, "\n"+elision) {
		t.Errorf("text left out after the cut is not marked:\n%s", out)
	}
	// a decision without headings or paragraphs (cut at --max-chars)
	dec := "Decision: the order export retries a failed batch and never splits it. The order export sends orders to the accounting system in batches of 100, and a batch either lands whole or not at all. A failed batch is retried with backoff up to five times, then parked for an operator. Each batch reads from a snapshot taken when it starts; a snapshot older than the newest change ends the batch with ErrStaleSnapshot, and a new snapshot is taken before the next try. Operators see parked batches on the export dashboard."
	out = cutText(dec, []string{"order", "export", "errstalesnapshot"}, 300)
	if !strings.Contains(out, "ends the batch with ErrStaleSnapshot") || len(out) > 300 {
		t.Errorf("a plain decision cut dropped the ErrStaleSnapshot sentence:\n%s", out)
	}
	// an underscore identifier: its words are split, its sentence is found
	out = cutText(strings.ReplaceAll(stockWritePath, "applyLevelDelta", "apply_level_delta"), []string{"calls", "apply", "level", "delta", "stock", "sync"}, 700)
	if !strings.Contains(out, "The only caller of apply_level_delta") {
		t.Errorf("an underscore identifier's sentence is dropped:\n%s", out)
	}
}

// TestCutTextKeepsHeadingAndOpening: a matching sentence keeps the
// heading it sits under, gaps are marked, and room left after the
// matching sentences is filled with the row's opening; with no word
// matching the cut is the start of the text, as before.
func TestCutTextKeepsHeadingAndOpening(t *testing.T) {
	t.Parallel()
	note := "# As built: warehouse stock sync\n\n## Overview\n\nThe stock sync keeps stock in step with the warehouse.\n\n## Write path\n\n" + stockWritePath + "\n\n## Operations\n\nOperators watch the sync lag per warehouse on the dashboard."
	out := cutText(note, []string{"applyleveldelta"}, 400)
	if !strings.Contains(out, "## Write path\n") || !strings.Contains(out, callerSentence) || len(out) > 400 {
		t.Fatalf("the matching sentence lost its heading:\n%s", out)
	}
	if !strings.Contains(out, "The stock sync keeps stock in step") || strings.Index(out, "## Overview") > strings.Index(out, "## Write path") {
		t.Errorf("room left is not filled with the opening, in text order:\n%s", out)
	}
	if !strings.Contains(out, "\n"+elision+"\n") || !strings.HasSuffix(out, "\n"+elision) {
		t.Errorf("gaps are not marked:\n%s", out)
	}
	if out := cutText(note, []string{"zebra"}, 300); !strings.HasPrefix(note, strings.TrimSuffix(out, "\n"+elision)) {
		t.Errorf("no word matching: the cut is not the start:\n%s", out)
	}
	// one sentence longer than room: cut around the identifier
	long := strings.Repeat("Filler words about the stock and the sync. ", 20) + "The " + strings.Repeat("very ", 80) + "long line names applyLevelDelta once and goes on " + strings.Repeat("and on ", 80) + "."
	if out := cutText(long, []string{"applyleveldelta", "stock"}, 200); !strings.Contains(out, "applyLevelDelta") || len(out) > 200 {
		t.Errorf("a sentence longer than room is not cut around the identifier (%d bytes):\n%s", len(out), out)
	}
}

// TestCommentCutKeepsMatchingSentence: a comment printed under its row
// because it holds a question word keeps that sentence when it is cut.
func TestCommentCutKeepsMatchingSentence(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("We looked at the import again today and nothing else changed in it. ", 4) + "The retry cap is now MaxImportTries = 7."
	r := Row{Matched: []string{"maximporttries"}, Comments: []Comment{{ID: "cmt_1", Body: body}}}
	if out := r.commentLines(); !strings.Contains(out, "MaxImportTries = 7") || strings.Contains(out, "\n   ") {
		t.Errorf("the comment cut dropped the matching sentence or broke its line:\n%s", out)
	}
}

func TestSplitSentences(t *testing.T) {
	t.Parallel()
	got := splitSentences("One. Two? Three! jobs/reconcile.go stays; v1.2 too. End")
	want := []string{"One.", "Two?", "Three!", "jobs/reconcile.go stays; v1.2 too.", "End"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}
