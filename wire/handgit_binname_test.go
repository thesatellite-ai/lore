package wire

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestHandGitBinaryIgnoresTheFileName: a store clone is set up for plain
// git (merge driver, pre-commit block) by every lore build, whatever its
// file is called; only a Go test binary is refused (devco D-333: a build
// named bin-lt left its clones with `*.json -merge` and no leak block).
func TestHandGitBinaryIgnoresTheFileName(t *testing.T) {
	noLore := func(string) (string, error) { return "", errors.New("not on PATH") }
	dir := t.TempDir()
	for _, name := range []string{"lore", "lore.exe", "lore-dev", "bin-lt", "lore_docs", "docs-verify"} {
		exe := filepath.Join(dir, name)
		bin, ok := handGitBinary(exe, false, noLore)
		if !ok {
			t.Errorf("a build named %s must set store clones up for plain git", name)
			continue
		}
		if want := "'" + filepath.ToSlash(exe) + "'"; bin != want {
			t.Errorf("%s: driver program %q, want %q", name, bin, want)
		}
	}
	for _, name := range []string{"wire.test", "lore", "bin-lt"} {
		if _, ok := handGitBinary(filepath.Join(dir, name), true, noLore); ok {
			t.Errorf("a Go test binary (named %s) must never write itself in as the hook and the driver", name)
		}
	}
}
