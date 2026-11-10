package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// namedFolderNotes names a --folder that is not there and the ones past
// the scan's folder limit.
func TestNamedFolderNotes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "code"), 0o700); err != nil {
		t.Fatal(err)
	}
	named := []string{"~/code", "~/gone", "", "~/code/**"}
	for range 8 {
		named = append(named, "~/code")
	}
	got := namedFolderNotes(named, home)
	want := []string{"folder ~/gone was not found: nothing was scanned there"}
	for range 4 {
		want = append(want, "folder ~/code was not scanned: the store scan walks at most 8 folders")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("notes\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
