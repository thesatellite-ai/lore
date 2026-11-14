package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoreFolderAtEmptyIsNotSetUp (D-20): an empty .lore/ (git never
// carries one; an earlier lore run left it) is no project, so a read or
// write there still refuses and nothing wires git into the repo; a .lore/
// with anything in it counts.
func TestLoreFolderAtEmptyIsNotSetUp(t *testing.T) {
	top := t.TempDir()
	if loreFolderAt(top) {
		t.Fatal("no .lore/ counted as set up")
	}
	if err := os.Mkdir(filepath.Join(top, loreFolderName), 0o700); err != nil {
		t.Fatal(err)
	}
	if loreFolderAt(top) {
		t.Fatal("an empty .lore/ counted as set up")
	}
	if err := os.WriteFile(filepath.Join(top, loreFolderName, "stores.yaml"), []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !loreFolderAt(top) {
		t.Fatal("a .lore/ holding stores.yaml did not count")
	}
}
