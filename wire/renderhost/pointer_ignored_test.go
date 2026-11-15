package renderhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// TestIgnoredPointerFile (devco:S-2): a repo that gitignores CLAUDE.md
// keeps lore's pointer on one machine, so every clone's Claude Code session
// misses LORE.md. init and render print a line saying so with the ignore
// rule and the two ways on; doctor's tool_pointer finding says the same
// and is local (CI's render --check, which judges committed files, notes
// it instead of failing on a file no clone can have).
func TestIgnoredPointerFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	keepLocalGit(t, root, "init", "-q")
	writeKeepLocal(t, root, ".gitignore", "/CLAUDE.md\n")
	writeKeepLocal(t, root, "CLAUDE.md", "# my own notes\n")

	reg, err := toolprofile.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := reg.Lookup(toolprofile.NameClaudeCode)
	targets := []contextTarget{{profile: claude, file: claude.PointerFile, known: true}}

	h := &Host{}
	var w strings.Builder
	h.ignoredPointerLines(ctx, &w, root, targets)
	for _, want := range []string{"CLAUDE.md is ignored by git (.gitignore:1:/CLAUDE.md)", "teammate's clone has no CLAUDE.md", "stop ignoring CLAUDE.md", "lore render"} {
		if !strings.Contains(w.String(), want) {
			t.Errorf("the init/render line misses %q:\n%s", want, w.String())
		}
	}

	sel, err := reg.Select([]string{toolprofile.NameClaudeCode}, rootProbe{root: root})
	if err != nil {
		t.Fatal(err)
	}
	got := pointerCopies(ctx, root, targetPick{sel: sel, targets: targets})
	if len(got) != 1 || got[0].Kind != opsrender.PointerMissing || !got[0].Local || !strings.Contains(got[0].Message, "is ignored by git") {
		t.Fatalf("doctor's finding for an ignored CLAUDE.md does not say it is ignored (or is not local): %+v", got)
	}

	// A tracked CLAUDE.md is not ignored, whatever .gitignore says: no line.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	keepLocalGit(t, root, "add", "CLAUDE.md")
	writeKeepLocal(t, root, ".gitignore", "/CLAUDE.md\n")
	w.Reset()
	h.ignoredPointerLines(ctx, &w, root, targets)
	if w.Len() != 0 {
		t.Errorf("a tracked CLAUDE.md is reported as ignored: %q", w.String())
	}
}
