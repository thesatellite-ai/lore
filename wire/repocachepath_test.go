package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoCachePathPerCheckout (owner item 35): the repo cache is keyed by
// the checkout's resolved path, never by its folder name or project: two
// checkouts named alike get two files, a symlink to a checkout gets that
// checkout's file.
func TestRepoCachePathPerCheckout(t *testing.T) {
	t.Parallel()
	root, home := t.TempDir(), t.TempDir()
	a, b := filepath.Join(root, "one", "app"), filepath.Join(root, "two", "app")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	ca, cb := RepoCachePath(home, a), RepoCachePath(home, b)
	if ca == cb || !strings.HasPrefix(filepath.Base(ca), "app-") || !strings.HasPrefix(filepath.Base(cb), "app-") {
		t.Fatalf("two checkouts named app: %s %s", ca, cb)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(a, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if got := RepoCachePath(home, link); got != ca {
		t.Fatalf("a symlink to a checkout: %s, want %s", got, ca)
	}
}
