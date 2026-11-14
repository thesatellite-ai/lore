package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestGeneratedClaudeNoteNamesTheWiredFile (devco J-3): CLAUDE.md carries
// another tool's "GENERATED FILE" banner, so it is skipped. With codex in
// the targets (after `lore render --add-target codex`) AGENTS.md gets the
// pointer, and the note names AGENTS.md instead of telling the person to
// run `lore render --add-target codex` again; with claude_code alone the
// note still offers that command.
func TestGeneratedClaudeNoteNamesTheWiredFile(t *testing.T) {
	root := t.TempDir()
	banner := "<!-- GENERATED FILE — do not edit directly; run make agents -->\n# Shop\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(banner), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# agents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := contextTarget{profile: builtin(t, toolprofile.NameClaudeCode), file: "CLAUDE.md", known: true}
	codex := contextTarget{profile: builtin(t, toolprofile.NameCodex), file: "AGENTS.md", known: true}
	out := filepath.Join(root, ".lore", "LORE.md")
	h := New(Options{})

	var alone strings.Builder
	if err := h.stitchPointers(&alone, root, []contextTarget{claude}, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alone.String(), "lore render --add-target codex (AGENTS.md)") {
		t.Errorf("claude_code alone: the note should offer AGENTS.md:\n%s", alone.String())
	}

	for _, run := range []string{"first", "second"} {
		var w strings.Builder
		if err := h.stitchPointers(&w, root, []contextTarget{claude, codex}, out); err != nil {
			t.Fatal(err)
		}
		got := w.String()
		if strings.Contains(got, "--add-target") || !strings.Contains(got, "the pointer is in AGENTS.md instead") || !strings.Contains(got, "claude_code reaches .lore/LORE.md only once") {
			t.Errorf("%s render with codex wired:\n%s", run, got)
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "CLAUDE.md")); err != nil || string(b) != banner {
		t.Errorf("the generated CLAUDE.md was changed: %q %v", b, err)
	}
}
