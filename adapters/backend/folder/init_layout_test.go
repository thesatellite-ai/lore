package folder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestInitLayout (plain-files round): Init makes a store a person can
// read: the marker and lore's README.md, no .lore folder and no empty data
// folder. When the store folder is a git work tree's top, lore's state
// folder (.lore-state: the lock and pending journal) goes into its
// .gitignore once; a plain folder gets no .gitignore.
func TestInitLayout(t *testing.T) {
	t.Parallel()
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "store")
	if err := Init(plain, rowfs.Marker{ID: id}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(plain)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != rowfs.ReadmeFile+","+rowfs.MarkerFile {
		t.Fatalf("a new folder store holds %v, want only the README and the marker", names)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is needed for this test: %v", err)
	}
	work := filepath.Join(t.TempDir(), "shelf")
	if out, err := exec.Command("git", "init", "--quiet", work).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.tmp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(work, rowfs.Marker{ID: id}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(work, ".gitignore"))
	if err != nil || !strings.HasPrefix(string(got), "*.tmp\n") || strings.Count(string(got), "/"+rowfs.StoreStateDir+"/") != 1 {
		t.Fatalf(".gitignore of a git work tree store:\n%s %v", got, err)
	}
	if err := ignoreStateDir(work); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(work, ".gitignore")); string(again) != string(got) {
		t.Fatalf("a second ignoreStateDir changed .gitignore:\n%s", again)
	}
}
