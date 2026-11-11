package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFullFileNamesItWithSlashes: the LORE.full.md line names a
// relative path with slashes on every OS, as the LORE.md line does
// (before, Windows printed ".lore\LORE.full.md").
func TestWriteFullFileNamesItWithSlashes(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir(".lore", 0o750); err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	if err := New(Options{}).writeFullFile(&w, filepath.Join(".lore", fullFileName), "placeholder\n", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), " .lore/"+fullFileName+" (") {
		t.Fatalf("line %q does not name .lore/%s", w.String(), fullFileName)
	}
}
