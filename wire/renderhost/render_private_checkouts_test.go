package renderhost

import (
	"context"
	"path/filepath"
	"testing"
)

// TestPrivateFilesSameForEveryCheckout pins B1-11: the private files live
// under the project's key, which every checkout of the project on this
// machine shares (a second clone, a copy), so they must not depend on the
// checkout's folder; with the folder name in them two checkouts rewrote
// each other's files and each found them stale (DEGRADED forever).
func TestPrivateFilesSameForEveryCheckout(t *testing.T) {
	ctx := context.Background()
	a, repo := privApp(t)
	h := New(Options{App: a, Home: t.TempDir()})
	one, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	two, err := h.planPrivate(ctx, filepath.Join(t.TempDir(), "second-clone"))
	if err != nil {
		t.Fatal(err)
	}
	if one.dir != two.dir || len(one.files) == 0 || len(one.files) != len(two.files) {
		t.Fatalf("plans differ: %s %d vs %s %d", one.dir, len(one.files), two.dir, len(two.files))
	}
	for i := range one.files {
		if one.files[i].body != two.files[i].body {
			t.Fatalf("%s differs between checkouts:\n%s\n----\n%s", filepath.Base(one.files[i].path), one.files[i].body, two.files[i].body)
		}
	}
}
