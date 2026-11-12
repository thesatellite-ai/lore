package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestStoreCheckoutIsNoProject (STORE layout 1.5): inside a lore store's
// checkout (lore-store.yaml beside .git) the host finds no project, so it
// never wires git (merge driver, attributes, hooks), never adds .gitignore
// lines and never refreshes a workflow pin there, even where a store
// folder holds a .lore folder of its own.
func TestStoreCheckoutIsNoProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git not installed")
	}
	store := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", store).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	folder := filepath.Join(store, "client_a")
	for _, d := range []string{filepath.Join(store, ".lore"), filepath.Join(folder, ".lore", "data")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store, "lore-store.yaml"), []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{store, folder} {
		if got := liveProjectRoot(dir); got != "" {
			t.Errorf("liveProjectRoot(%s) = %q inside a store", dir, got)
		}
		if got := projectRoot(t.Context(), dir); got != "" {
			t.Errorf("projectRoot(%s) = %q inside a store", dir, got)
		}
	}
	// the same tree without the marker is a code repo with a project
	if err := os.Remove(filepath.Join(store, "lore-store.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := liveProjectRoot(folder); got == "" {
		t.Error("a code repo's project folder was not found once the store marker is gone")
	}
}
