package wire

// dryhome_readonly_test.go — F-6: on a CI runner whose HOME lore cannot
// write, every read failed E_READ_ONLY_FS creating ~/.lore/cache. The
// caches now go to a scratch folder removed when the command ends.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritableHomeReadOnlyHomeUsesScratch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through permissions")
	}
	t.Setenv("TMPDIR", t.TempDir())
	parent := t.TempDir()
	home := filepath.Join(parent, ".lore")
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	t.Cleanup(RemoveDryScratch)

	got := writableHome(home)
	if got == home || !strings.HasPrefix(got, os.Getenv("TMPDIR")) {
		t.Fatalf("writableHome(%s) = %s, want a scratch folder under TMPDIR", home, got)
	}
	path, err := combinedCachePath(home, "/work/app")
	if err != nil {
		t.Fatalf("a cache path on a read-only home: %v", err)
	}
	if !strings.HasPrefix(path, got) {
		t.Errorf("cache %s is not in the scratch home %s", path, got)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("the read-only home was written: %v", err)
	}
	RemoveDryScratch()
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("scratch home %s left behind", got)
	}
}

func TestWritableHomeWritableHomeStays(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lore")
	if got := writableHome(home); got != home {
		t.Fatalf("writableHome(%s) = %s on a writable home", home, got)
	}
	entries, _ := os.ReadDir(filepath.Dir(home))
	if len(entries) != 0 {
		t.Errorf("the probe left %d file(s) beside the home", len(entries))
	}
}
