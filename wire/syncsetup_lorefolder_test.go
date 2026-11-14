package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoreFolderUpTo: a git repo with no .lore from the folder up to its
// top is synced as the setup's stores (setupSyncHere); a .lore at the top
// or in a folder between (a monorepo project) keeps the repo sync.
func TestLoreFolderUpTo(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	sub := filepath.Join(top, "svc", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if loreFolderUpTo(sub, top) || loreFolderUpTo(top, top) {
		t.Fatal("a repo with no .lore anywhere counts as a lore project")
	}
	if err := os.MkdirAll(filepath.Join(top, "svc", ".lore", "data"), 0o755); err != nil { // an empty .lore/ is no project (loreFolderAt, D-20)
		t.Fatal(err)
	}
	if !loreFolderUpTo(sub, top) {
		t.Error("a .lore in a folder above the cwd (monorepo project) is not seen")
	}
	if loreFolderUpTo(top, top) {
		t.Error("a .lore below the cwd counts for the top")
	}
	if !loreFolderUpTo(sub, "") {
		t.Error("an unknown top must leave the repo sync as it was")
	}
}
