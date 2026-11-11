//go:build userscen

package userscen

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// narrowPath is a PATH with only the given folders first, then git and
// the system tools it needs: no other lore (a developer's own one would
// make doctor warn about two binaries). On Windows the system tools are
// System32 and Git for Windows' usr\bin and mingw64\bin: "/usr/bin" and
// "/bin" name nothing there, and without them git's sh crashed (exit
// 0xC0000005) on the first clone.
func narrowPath(t *testing.T, first ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dirs := append(append([]string{}, first...), filepath.Dir(git))
	if runtime.GOOS != "windows" {
		dirs = append(dirs, "/usr/bin", "/bin")
		return strings.Join(dirs, string(os.PathListSeparator))
	}
	// git is <Git>\cmd\git.exe (or <Git>\bin, <Git>\mingw64\bin): find <Git>.
	for root := filepath.Dir(filepath.Dir(git)); ; root = filepath.Dir(root) {
		if _, err := os.Stat(filepath.Join(root, "usr", "bin", "sh.exe")); err == nil {
			dirs = append(dirs, filepath.Join(root, "mingw64", "bin"), filepath.Join(root, "usr", "bin"))
			break
		}
		if filepath.Dir(root) == root {
			break
		}
	}
	if sys := os.Getenv("SystemRoot"); sys != "" {
		dirs = append(dirs, filepath.Join(sys, "System32"), sys)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}
