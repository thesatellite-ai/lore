package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/foldercase"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestFolderRenameNonASCII (devco Q-13): a folder renamed with a non-ASCII
// name (or a space) was not recognised as a rename: git quotes such paths
// in --name-status output ("src/\303\247izelge/…"), so its scoped rules
// silently fell to the root while an ASCII rename in the same commit was
// caught. Both renames are found now, from the raw -z paths.
//
// devco D-414: the ASCII rename is case-only (src/Charts to src/charts).
// On a case-insensitive file system (APFS) a one-step `git mv` of a folder
// to its other-case name fails, so the test renames in two steps, as a
// person on a Mac must; and lore must see the rename there although a stat
// of src/Charts still finds the folder. The "folding" case runs that macOS
// path on any OS through foldercase.Folding.
func TestFolderRenameNonASCII(t *testing.T) {
	for _, tc := range []struct {
		name string
		view foldercase.FS
	}{{"this file system", foldercase.OS{}}, {"folding", foldercase.Folding{}}} {
		t.Run(tc.name, func(t *testing.T) { folderRenameCase(t, tc.view) })
	}
}

func folderRenameCase(t *testing.T, view foldercase.FS) {
	top := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", top, "-c", "user.name=Ann Able", "-c", "user.email=ann@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	const uni, uniNew = "src/çizelge-日本語", "src/çizelge-日本語-v2"
	const ascii, asciiNew = "src/Charts", "src/charts"
	for _, f := range []string{uni + "/tablo ü.ts", ascii + "/a.ts"} {
		p := filepath.Join(top, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "folders")
	git("mv", uni, uniNew)
	git("mv", ascii, ascii+".tmp") // case-only: two steps, as APFS needs
	git("mv", ascii+".tmp", asciiNew)
	git("commit", "-q", "-m", "rename folders")

	ctx := context.Background()
	dirs := foldercase.New(view, top)
	if _, folding := view.(foldercase.Folding); folding && !dirs.Folds() {
		t.Fatal("foldercase.New did not detect the folding view")
	}
	for old, want := range map[string]string{uni: uniNew, ascii: asciiNew} {
		if got := folderRenamedTo(ctx, dirs, top, old); got != want {
			t.Errorf("folderRenamedTo(%q) = %q, want %q", old, got, want)
		}
	}
	rows := []row.Row{scopeRow(t, uni, false), scopeRow(t, ascii, false), scopeRow(t, asciiNew, false)}
	got := goneScopes(ctx, dirs, top, rows)
	var gone []string
	for _, g := range got {
		gone = append(gone, g.Path+" -> "+g.RenamedTo)
	}
	if want := []string{uni + " -> " + uniNew, ascii + " -> " + asciiNew}; !slices.Equal(gone, want) {
		t.Errorf("goneScopes = %q, want %q (the case-only rename is a rename; the new folder is not gone)", gone, want)
	}
	removed := HeadRemovedPaths(ctx, top)
	for _, p := range []string{uni + "/tablo ü.ts", ascii + "/a.ts"} {
		if !slices.Contains(removed, p) {
			t.Errorf("HeadRemovedPaths = %q, want %q among them", removed, p)
		}
	}
}
