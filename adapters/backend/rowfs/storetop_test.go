package rowfs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStoreTop (STORE layout 1.5): a git store clone is recognised from
// any folder inside it by lore-store.yaml beside .git; a code repo is not
// a store even with a lore-store.yaml deeper down; a folder store (no git)
// is found by its marker; StoreFolderAt names the folder a dir sits in.
func TestStoreTop(t *testing.T) {
	mk := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(p string) {
		t.Helper()
		mk(filepath.Dir(p))
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// root unresolved on purpose: on macOS t.TempDir is under /var, a link
	// to /private/var, and StoreTop returns the resolved top (D-389)
	root := t.TempDir()
	clone := filepath.Join(root, "stores", "acme", "notes")
	mk(filepath.Join(clone, ".git"))
	touch(filepath.Join(clone, MarkerFile))
	touch(filepath.Join(clone, "client_a", MetaFileName))
	deep := filepath.Join(clone, "client_a", "tasks")
	mk(deep)
	for _, dir := range []string{clone, filepath.Join(clone, "client_a"), deep} {
		top, isGit, ok := StoreTop(dir)
		if !ok || !isGit || !samePathForTest(t, top, clone) {
			t.Fatalf("StoreTop(%s) = %s %v %v, want %s git", dir, top, isGit, ok, clone)
		}
	}
	if f := StoreFolderAt(clone, deep); f != "client_a" {
		t.Fatalf("StoreFolderAt(deep) = %q, want client_a", f)
	}
	if f := StoreFolderAt(clone, clone); f != FolderRoot {
		t.Fatalf("StoreFolderAt(top) = %q, want the root", f)
	}
	code := filepath.Join(root, "code", "app")
	mk(filepath.Join(code, ".git"))
	touch(filepath.Join(code, "fixtures", MarkerFile))
	if _, _, ok := StoreTop(filepath.Join(code, "fixtures")); ok {
		t.Fatal("a code repo with a lore-store.yaml in a subfolder is not a store")
	}
	shelf := filepath.Join(root, "shelf")
	touch(filepath.Join(shelf, MarkerFile))
	mk(filepath.Join(shelf, "topic"))
	if top, isGit, ok := StoreTop(filepath.Join(shelf, "topic")); !ok || isGit || !samePathForTest(t, top, shelf) {
		t.Fatalf("folder store: %s %v %v", top, isGit, ok)
	}
}

// samePathForTest reports whether a and b name the same folder once
// symlinks are resolved: StoreTop returns resolved paths, a test builds its
// own under t.TempDir, which on macOS sits under the /var -> /private/var
// link (D-389).
func samePathForTest(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	return ra == rb
}

// TestStoreTopThroughLink pins D-389 on every OS: a store reached through
// a symlinked folder (as every t.TempDir on macOS is, under /var ->
// /private/var) is found with its resolved top, and StoreFolderAt names
// the folder whether it is given the linked or the resolved top.
func TestStoreTopThroughLink(t *testing.T) {
	base := t.TempDir()
	realTop := filepath.Join(base, "real", "notes")
	for _, p := range []string{filepath.Join(realTop, ".git"), filepath.Join(realTop, "client_a", "tasks")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(realTop, MarkerFile), filepath.Join(realTop, "client_a", MetaFileName)} {
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "real"), link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	linked := filepath.Join(link, "notes")
	deep := filepath.Join(linked, "client_a", "tasks")
	top, isGit, ok := StoreTop(deep)
	if !ok || !isGit || !samePathForTest(t, top, realTop) {
		t.Fatalf("StoreTop(%s) = %s %v %v, want %s git", deep, top, isGit, ok, realTop)
	}
	for _, givenTop := range []string{linked, top} {
		if f := StoreFolderAt(givenTop, deep); f != "client_a" {
			t.Errorf("StoreFolderAt(%s, deep) = %q, want client_a", givenTop, f)
		}
	}
}
