package batch

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/docnotes"
	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/schema"
)

// devco:D-940, D-962: a section a stored note already says is skipped
// whole, a decision said twice in one import is skipped, and inside a
// fact section only the paragraph said before is dropped: the rest of the
// section is kept under its heading.
func TestDocsImportDropsRepeats(t *testing.T) {
	split := func(doc string) []draft {
		var out []draft
		for _, p := range docnotes.Split(doc) {
			out = append(out, newDraft(p, "TECHNICAL.md", t.TempDir(), t.TempDir()))
		}
		return out
	}
	held := "Invoices are rendered as PDF and kept for ninety days."
	drafts := split("# Billing\n\n## Queue\n\nThe worker pulls jobs every five seconds from the queue.\n\n" +
		"## Invoices\n\n" + held + "\n\nThings that expire them:\n\n- a refund,\n- a manual purge.\n\n" +
		"## Locks\n\nWe chose Postgres advisory locks over Redis because one fewer service runs.\n\n" +
		"## Again\n\nWe chose Postgres advisory locks over Redis because one fewer service runs.\n\n" +
		"The worker  pulls jobs every five seconds from the queue\n\nIt parks a job after three failed tries in a row.\n")
	existing := []neardup.Item{{Table: string(schema.TableMemories), ID: "mem_held", Title: neardup.Headline("", "", held), Body: held}}
	keep, skipped := dropRepeats(drafts, existing)
	var got []string
	for _, d := range keep {
		got = append(got, d.piece.Title+" @"+d.evidence)
	}
	want := []string{"Billing › Queue @TECHNICAL.md:5", "Billing › Invoices @TECHNICAL.md:11", "We chose Postgres advisory locks over Redis because one fewer service runs @TECHNICAL.md:18",
		"Billing › Again @TECHNICAL.md:26"}
	if !slices.Equal(got, want) {
		t.Fatalf("kept %q,\nwant %q", got, want)
	}
	if keep[1].piece.Text != "Billing › Invoices\n\nThings that expire them:\n\n- a refund,\n- a manual purge." {
		t.Fatalf("the Invoices section kept %q, want its list under its heading without the stored paragraph", keep[1].piece.Text)
	}
	if len(skipped) != 3 || skipped[0].Existing != "mem_held" || skipped[0].Evidence != "TECHNICAL.md:9" ||
		!strings.Contains(skipped[1].Why, "repeats TECHNICAL.md:18") || !strings.Contains(skipped[2].Why, "repeats TECHNICAL.md:5") {
		t.Fatalf("skipped %+v, want line 9 as mem_held, the second decision and the repeated worker paragraph as repeats", skipped)
	}
	// the per-doc counts (H-221 addendum): drafted by kind, skipped by reason
	docs := []DocCount{{File: "TECHNICAL.md", Boilerplate: 1}}
	countDocs(docs, keep, skipped)
	want1 := "TECHNICAL.md: would draft 3 memories, 1 decision; skipped 1 already stored, 2 repeats in this import, 1 boilerplate section\n"
	if got := docs[0].line(true); got != want1 {
		t.Fatalf("count line = %q,\nwant %q", got, want1)
	}
	if got := (DocCount{File: "README.md", Memories: 1}).line(false); got != "README.md: drafted 1 memory, 0 decisions; skipped nothing\n" {
		t.Fatalf("count line = %q", got)
	}
	// a re-import: every section the store now holds is skipped whole
	var stored []neardup.Item
	for i, d := range keep {
		stored = append(stored, neardup.Item{Table: pieceItem(i, d).Table, ID: "row" + strconv.Itoa(i), Title: d.piece.Title, Body: d.piece.Text})
	}
	if again, sk := dropRepeats(split("# Billing\n\n## Queue\n\nThe worker pulls jobs every five seconds from the queue.\n"), stored); len(again) != 0 || len(sk) != 1 || sk[0].Existing != "row0" {
		t.Fatalf("re-import kept %+v, skipped %+v", again, sk)
	}
	// two docs' sections with the same heading are not the same note
	other, _ := dropRepeats(split("## Queue\n\nThe billing queue is drained by a cron job at midnight every day.\n"),
		[]neardup.Item{{Table: string(schema.TableMemories), ID: "mem_q", Title: "Queue", Body: "Queue\n\nThe mail queue is a Redis list."}})
	if len(other) != 1 {
		t.Fatalf("a section titled like a stored note was skipped: %+v", other)
	}
}

// devco:D-940: the idempotency key depends on the folded text only, so the
// same paragraph moved to another line keeps its key.
func TestDocsImportKeyFollowsTheText(t *testing.T) {
	a := draftKey(docnotes.Piece{Kind: docnotes.Fact, Line: 3, Text: "Invoices are rendered as PDF."})
	b := draftKey(docnotes.Piece{Kind: docnotes.Fact, Line: 40, Text: "invoices are rendered as  PDF"})
	c := draftKey(docnotes.Piece{Kind: docnotes.Fact, Line: 3, Text: "Invoices are rendered as HTML."})
	if a != b || a == c || !strings.HasPrefix(a, keyPrefix) {
		t.Fatalf("keys %q %q %q", a, b, c)
	}
}

// devco:D-940: a code reference becomes evidence only when the file is in
// the repo with at least that many lines; nothing outside the repo counts.
func TestDocsImportEvidenceNamesRealCodeLines(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "queue", "worker.go"), []byte("package queue\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(root, "docs", "TECHNICAL.md")
	body := "# Q\n\nThe worker runs here (internal/queue/worker.go:3) and was moved from gone/old.go:2 last year.\n\n" +
		"The loop past the end is internal/queue/worker.go:99 in the old layout of the repo.\n"
	if err := os.WriteFile(doc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, rel, _, err := readDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "docs/TECHNICAL.md" {
		t.Fatalf("rel = %q, want the repo-relative path", rel)
	}
	if len(ds) != 1 || ds[0].evidence != "docs/TECHNICAL.md:3, internal/queue/worker.go:3" {
		t.Fatalf("evidence = %+v, want one section naming only the code line that exists", ds)
	}
	if _, _, _, err = readDoc(filepath.Join(root, "missing.md")); err == nil || !strings.Contains(err.Error(), "E_NOT_FOUND") {
		t.Fatalf("a missing doc: %v", err)
	}
}
