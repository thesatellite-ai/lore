package renderhost

import (
	"strings"
	"testing"

	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// TestUntrackedFullIsANote (devco O-8): a LORE.full.md the repo never
// committed is a note for render --check (kept out of staleError, so no
// E_REFUSED) whose fix renders it and adds it to git.
func TestUntrackedFullIsANote(t *testing.T) {
	f := untrackedFullFinding(".lore/LORE.full.md")
	tracked, local := splitLocalCopies([]opsrender.CopyFinding{f})
	if len(tracked) != 0 || len(local) != 1 {
		t.Fatalf("the untracked full file is judged as a tracked copy: tracked=%v local=%v", tracked, local)
	}
	if err := staleError(tracked); err != nil {
		t.Fatalf("render --check fails on the untracked full file: %v", err)
	}
	if !strings.Contains(f.Fix, "git add .lore/LORE.full.md") || !strings.Contains(f.Message, "git does not track it") {
		t.Fatalf("the note does not name the cause and the fix: %q / %q", f.Message, f.Fix)
	}
}
