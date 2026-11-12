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
	root := t.TempDir()
	clone := filepath.Join(root, "stores", "acme", "notes")
	mk(filepath.Join(clone, ".git"))
	touch(filepath.Join(clone, MarkerFile))
	touch(filepath.Join(clone, "client_a", MetaFileName))
	deep := filepath.Join(clone, "client_a", "tasks")
	mk(deep)
	for _, dir := range []string{clone, filepath.Join(clone, "client_a"), deep} {
		top, isGit, ok := StoreTop(dir)
		if !ok || !isGit || top != clone {
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
	if top, isGit, ok := StoreTop(filepath.Join(shelf, "topic")); !ok || isGit || top != shelf {
		t.Fatalf("folder store: %s %v %v", top, isGit, ok)
	}
}
