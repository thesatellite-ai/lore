package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveEmptyDirsKeepsFiles pins B4-20: the empty <host>/<owner>/
// folders old clones left go (clones/ too once empty); a folder holding
// a file stays.
func TestRemoveEmptyDirsKeepsFiles(t *testing.T) {
	clones := filepath.Join(t.TempDir(), "clones")
	if err := os.MkdirAll(filepath.Join(clones, "git.example", "acme"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(clones, "git.example", "pat", "kept"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clones, "git.example", "pat", "kept", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := removeEmptyDirs(clones); n != 1 {
		t.Fatalf("removed %d, want 1 (acme)", n)
	}
	if _, err := os.Stat(filepath.Join(clones, "git.example", "pat", "kept", "f")); err != nil {
		t.Fatal("a file went")
	}
	if err := os.Remove(filepath.Join(clones, "git.example", "pat", "kept", "f")); err != nil {
		t.Fatal(err)
	}
	if n := removeEmptyDirs(clones); n != 4 {
		t.Fatalf("removed %d, want 4 (kept, pat, git.example, clones)", n)
	}
	if _, err := os.Stat(clones); !os.IsNotExist(err) {
		t.Fatal("an empty clones/ stays")
	}
}

// TestHomeLayoutNamesRound2Files pins B4-20: lore's own layout-move
// record and lock, and the 0.1.17 per-project renders, are lore's.
func TestHomeLayoutNamesRound2Files(t *testing.T) {
	for _, rel := range []string{"state/layout-move.json", "state/layout-move.lock", "render/prj_01a1d138864e7acebca878568d8db213.md", "cache/links-acme.json"} {
		if _, ok := matchHome(rel, false); !ok {
			t.Fatalf("%s is not lore's", rel)
		}
	}
}
