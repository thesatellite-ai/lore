//go:build !windows

package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoreHomeAndPersonalStoreAreOwnerOnly: a lore home an older lore (or
// a hand-made mkdir) left 0755 is closed to group and others when lore
// opens it, and the local personal store folder is created 0700 with the
// home above it closed too; a symlinked home is left as the person set it.
func TestLoreHomeAndPersonalStoreAreOwnerOnly(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".lore")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o755); err != nil { // the umask may have cut it
		t.Fatal(err)
	}
	closeLoreHome(home)
	if fi, _ := os.Stat(home); fi.Mode().Perm() != 0o700 {
		t.Errorf("lore home is %v after closeLoreHome, want 0700", fi.Mode().Perm())
	}
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	personal := filepath.Join(home, "stores", "me")
	if err := os.MkdirAll(filepath.Dir(personal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(personal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := privatePersonalDir(personal); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{personal, filepath.Dir(personal)} {
		if fi, _ := os.Stat(p); fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want owner-only", p, fi.Mode().Perm())
		}
	}
	target := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-home")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	closeLoreHome(link)
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o755 {
		t.Errorf("a symlinked home's target was changed to %v", fi.Mode().Perm())
	}
}
