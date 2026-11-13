package renderhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestStitchPointersLinkedClaudeToAgents (devco D-23): with CLAUDE.md a
// link to AGENTS.md, every render wrote the file twice (Claude's block,
// then Codex's over it) and the file kept only the Codex sentence. Now the
// two targets share one block with the @import and the sentence, the link
// stays, and a second render writes nothing ("pointer present").
// Stitching each target on its own (the old loop) fails
// on the missing @import and on the second render's write.
func TestStitchPointersLinkedClaudeToAgents(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# agents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := link(t, "AGENTS.md", filepath.Join(root, "CLAUDE.md"))
	targets := []contextTarget{
		{profile: builtin(t, toolprofile.NameClaudeCode), file: "CLAUDE.md", known: true},
		{profile: builtin(t, toolprofile.NameCodex), file: "AGENTS.md", known: true},
	}
	out := filepath.Join(root, ".lore", "LORE.md")
	h := New(Options{})
	var w strings.Builder
	if err := h.stitchPointers(&w, root, targets, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, "@.lore/LORE.md\n") || !strings.Contains(got, "Read `.lore/LORE.md`") || strings.Count(got, toolprofile.PointerStartMarker) != 1 {
		t.Fatalf("AGENTS.md (CLAUDE.md through the link):\n%s", got)
	}
	assertStillLink(t, at)
	if strings.Count(w.String(), "✓") != 1 {
		t.Errorf("one file, one write: %s", w.String())
	}
	var again strings.Builder
	if err := h.stitchPointers(&again, root, targets, out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(again.String(), "✓") || !strings.Contains(again.String(), "(pointer present)") {
		t.Errorf("second render wrote again: %s", again.String())
	}
}

// TestStitchPointerPackageNamesOwnFile (devco D-22): in a package project
// (its own .lore below the git top) the read-style pointer said
// "Read `.lore/LORE.md` (at the repository root)", the root project's
// file. It now names the package's file from the repository root.
// With FromRoot taken from the project root the sentence
// names `.lore/LORE.md` and the test fails.
func TestStitchPointerPackageNamesOwnFile(t *testing.T) {
	top := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", top).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	pkg := filepath.Join(top, "workers", "extraction")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	t0 := contextTarget{profile: builtin(t, toolprofile.NameCodex), file: "AGENTS.md", known: true}
	if err := New(Options{}).stitchPointer(&w, pkg, t0, filepath.Join(pkg, ".lore", "LORE.md")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(pkg, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Read `workers/extraction/.lore/LORE.md` (at the repository root)") {
		t.Fatalf("package pointer:\n%s", b)
	}
}
