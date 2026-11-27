package app

import (
	"testing"

	opslint "github.com/thesatellite-ai/lore/ops/lint"
)

// TestLintFixNounsHaveArchive (devco:D-1027): every noun near_duplicate
// and stale_claim print in their fix (`lore <noun> archive --id <id>`) is
// a command with an archive verb taking --id, so the printed fix runs.
func TestLintFixNounsHaveArchive(t *testing.T) {
	root := driftRoot(t)
	nouns := append(opslint.ArchiveNouns(), opslint.ClaimNouns()...)
	for _, n := range nouns {
		code := "lore " + n + " archive --id mem_0"
		if p := checkInvocation(root, code); p != "" {
			t.Errorf("%s: %s", code, p)
		}
		cmd, _, err := root.Find([]string{n, "archive"})
		if err != nil || cmd == nil || cmd.Name() != "archive" || cmd.Flags().Lookup("id") == nil {
			t.Errorf("lore %s archive --id is not a command (%v)", n, err)
		}
	}
	if len(opslint.ClaimNouns()) <= len(opslint.ArchiveNouns()) {
		t.Fatalf("stale_claim must scan more kinds than near_duplicate: %v", opslint.ClaimNouns())
	}
}
