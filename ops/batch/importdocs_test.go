package batch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/docnotes"
	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/schema"
)

// devco:D-940: a piece said twice in one import, and one a stored note
// already says, are skipped and listed; the rest are drafted.
func TestDocsImportDropsRepeats(t *testing.T) {
	mk := func(line int, kind docnotes.Kind, text string) draft {
		p := docnotes.Piece{Kind: kind, Line: line, Text: text}
		if kind == docnotes.Decision {
			p.Title = docnotes.Title(text)
		}
		return draft{piece: p, docRef: "TECHNICAL.md:" + string(rune('0'+line)), evidence: "TECHNICAL.md:" + string(rune('0'+line))}
	}
	drafts := []draft{
		mk(1, docnotes.Fact, "The worker pulls jobs every five seconds from the queue."),
		mk(2, docnotes.Fact, "The  worker pulls jobs every five seconds from the queue"),
		mk(3, docnotes.Fact, "Invoices are rendered as PDF and kept for ninety days."),
		mk(4, docnotes.Decision, "We chose Postgres advisory locks over Redis because one fewer service."),
	}
	existing := []neardup.Item{{Table: string(schema.TableMemories), ID: "mem_held", Title: neardup.Headline("", "", "Invoices are rendered as PDF and kept for ninety days."),
		Body: "Invoices are rendered as PDF and kept for ninety days."}}
	keep, skipped := dropRepeats(drafts, existing)
	if len(keep) != 2 || keep[0].piece.Line != 1 || keep[1].piece.Line != 4 {
		t.Fatalf("kept %+v, want lines 1 and 4", keep)
	}
	if len(skipped) != 2 || !strings.Contains(skipped[0].Why, "repeats TECHNICAL.md:1") || skipped[1].Existing != "mem_held" {
		t.Fatalf("skipped %+v, want line 2 as a repeat of line 1 and line 3 as mem_held", skipped)
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
	ds, rel, err := readDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "docs/TECHNICAL.md" {
		t.Fatalf("rel = %q, want the repo-relative path", rel)
	}
	if len(ds) != 2 || ds[0].evidence != "docs/TECHNICAL.md:3, internal/queue/worker.go:3" || ds[1].evidence != "docs/TECHNICAL.md:5" {
		t.Fatalf("evidence = %q", []string{ds[0].evidence, ds[len(ds)-1].evidence})
	}
	if _, _, err = readDoc(filepath.Join(root, "missing.md")); err == nil || !strings.Contains(err.Error(), "E_NOT_FOUND") {
		t.Fatalf("a missing doc: %v", err)
	}
}
