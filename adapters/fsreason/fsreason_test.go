package fsreason

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitfault"
)

// TestOfWordsAlikeOnEveryOS: a missing file, a path through a file, a
// folder read as a file: the same words on every operating system (before,
// Windows said "The system cannot find the file specified." and
// "Incorrect function.").
func TestOfWordsAlikeOnEveryOS(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(p string) error {
		_, err := os.ReadFile(p)
		return err
	}
	for _, c := range []struct{ name, path, want string }{
		{"missing", filepath.Join(dir, "gone.md"), NotExist},
		{"through a file", filepath.Join(file, "x.md"), NotDir},
		{"a folder", dir, IsDir},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := read(c.path)
			if err == nil {
				t.Fatal("read succeeded")
			}
			if got := Of(c.path, err); got != c.want {
				t.Fatalf("Of = %q, want %q (err %v)", got, c.want, err)
			}
			if got := Of("", err); got != c.want {
				t.Fatalf("Of from the PathError = %q, want %q", got, c.want)
			}
		})
	}
}

// B3-22: a folder this user may not list is unreadable, a missing one is
// absent, a normal one readable.
func TestGitPathState(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.git")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := GitPathState(locked); got != gitfault.PathReadable {
		t.Fatalf("readable folder: %d", got)
	}
	if got := GitPathState(filepath.Join(dir, "none.git")); got != gitfault.PathAbsent {
		t.Fatalf("missing folder: %d", got)
	}
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		return // root reads everything; Windows has no mode 000
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if got := GitPathState(locked); got != gitfault.PathUnreadable {
		t.Fatalf("mode 000 folder: %d", got)
	}
}
