package renderhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestPrivateProjectNoPointerFinding (devco:D-856): in a project whose
// .lore/ git ignores (here only through the clone's .git/info/exclude),
// render never puts the pointer into a tracked tool file (D-500), so
// doctor's tool_pointer finding with "fix: lore render" pointed at a
// command that changed nothing; it reports nothing there. The same repo
// without the exclude still gets the finding.
func TestPrivateProjectNoPointerFinding(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	keepLocalGit(t, root, "init", "-q")
	writeKeepLocal(t, root, ".lore/LORE.md", "# lore\n")
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte(".lore/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := toolprofile.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	codex, ok := reg.Lookup("codex")
	if !ok {
		t.Fatal("no codex profile")
	}
	targets := []contextTarget{{profile: codex, file: codex.PointerFile, known: true}}
	sel, err := reg.Select([]string{"codex"}, rootProbe{root: root})
	if err != nil {
		t.Fatal(err)
	}
	pick := targetPick{sel: sel, targets: targets}
	if got := pointerCopies(ctx, root, pick); len(got) != 0 {
		t.Fatalf("a private project gets a pointer finding render cannot clear: %+v", got)
	}
	if err := os.WriteFile(exclude, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pointerCopies(ctx, root, pick); len(got) != 1 {
		t.Fatalf("a shared project lacking the pointer: %+v, want one finding", got)
	}
}
