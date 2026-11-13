package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestChangedFiles (B3-32): the refresh hook names only the render files
// whose bytes changed; a render that wrote the same bytes is silent.
func TestChangedFiles(t *testing.T) {
	wd := t.TempDir()
	md := filepath.Join(wd, ".lore", "LORE.md")
	full := filepath.Join(wd, ".lore", "LORE.full.md")
	if err := os.MkdirAll(filepath.Dir(md), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{md, full} {
		if err := os.WriteFile(f, []byte("same\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{md, full}
	before := readAll(files)
	if got := changedFiles(wd, files, before); len(got) != 0 {
		t.Fatalf("nothing changed, got %v", got)
	}
	if err := os.WriteFile(md, []byte("new rows\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := changedFiles(wd, files, before); !reflect.DeepEqual(got, []string{".lore/LORE.md"}) {
		t.Fatalf("changed = %v, want [.lore/LORE.md]", got)
	}
}
