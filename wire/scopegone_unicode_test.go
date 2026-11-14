package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestFolderRenameNonASCII (devco Q-13): a folder renamed with a non-ASCII
// name (or a space) was not recognised as a rename: git quotes such paths
// in --name-status output ("src/\303\247izelge/…"), so its scoped rules
// silently fell to the root while an ASCII rename in the same commit was
// caught. Both renames are found now, from the raw -z paths.
func TestFolderRenameNonASCII(t *testing.T) {
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
	git("mv", ascii, asciiNew)
	git("commit", "-q", "-m", "rename folders")
	ctx := context.Background()
	for old, want := range map[string]string{uni: uniNew, ascii: asciiNew} {
		if got := FolderRenamedTo(ctx, top, old); got != want {
			t.Errorf("FolderRenamedTo(%q) = %q, want %q", old, got, want)
		}
	}
	removed := HeadRemovedPaths(ctx, top)
	for _, p := range []string{uni + "/tablo ü.ts", ascii + "/a.ts"} {
		if !slices.Contains(removed, p) {
			t.Errorf("HeadRemovedPaths = %q, want %q among them", removed, p)
		}
	}
}
