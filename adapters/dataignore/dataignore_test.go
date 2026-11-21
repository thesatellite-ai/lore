package dataignore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestIsOSJunk: the clutter operating systems drop into browsed folders is
// junk at any depth; lore's own files (and names merely containing a junk
// name) are not. Break-verified: dropping "._*" from OSJunkPatterns fails
// the AppleDouble cases.
func TestIsOSJunk(t *testing.T) {
	for name, want := range map[string]bool{
		".DS_Store":                      true,
		".lore/data/memories/.DS_Store":  true,
		"._foo":                          true,
		".lore/data/._mem_1.json":        true,
		"Thumbs.db":                      true,
		"desktop.ini":                    true,
		"Desktop.ini":                    true,
		".lore/data/_meta.json":          false,
		".lore/data/memories/mem_1.json": false,
		"x.DS_Store":                     false,
		"_foo":                           false,
	} {
		if got := IsOSJunk(name); got != want {
			t.Errorf("IsOSJunk(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestIsRowFile: a .json file that is not clutter.
func TestIsRowFile(t *testing.T) {
	for name, want := range map[string]bool{
		".lore/data/memories/mem_1.json": true,
		".lore/data/_meta.json":          true,
		".lore/data/._mem_1.json":        false,
		".lore/data/.DS_Store":           false,
		".lore/data/notes.txt":           false,
	} {
		if got := IsRowFile(name); got != want {
			t.Errorf("IsRowFile(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestIgnoredOutsideWorkTree (devco:D-876): asked in a folder with no git
// repository around it, git ignores nothing there: no paths, no error
// (it was E_INTERNAL "git check-ignore ... not a git repository").
func TestIgnoredOutsideWorkTree(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	got, err := Ignored(context.Background(), dir, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("Ignored outside a work tree = %v, %v; want nothing, no error", got, err)
	}
}
