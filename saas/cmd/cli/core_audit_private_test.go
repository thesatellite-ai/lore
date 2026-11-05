package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenPrivateLoreDBRelativePath pins FB-SESS-1: a relative lore.db path
// (".lore/lore.db", or a v0.1.17 pointer's "../client.db") must open, both
// read-only and read-write. It once became "file://../client.db", whose
// authority SQLite refuses, and every write refreshing the tool session ids
// failed E_INTERNAL.
func TestOpenPrivateLoreDBRelativePath(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	ctx := context.Background()
	for _, rel := range []string{".lore/lore.db", "../client.db"} {
		w, err := openPrivateLoreDB(rel, false)
		if err != nil {
			t.Fatalf("open %s read-write: %v", rel, err)
		}
		if _, xerr := w.ExecContext(ctx, `CREATE TABLE t (x INTEGER)`); xerr != nil {
			t.Fatalf("create in %s: %v", rel, xerr)
		}
		if cerr := w.Close(); cerr != nil {
			t.Fatal(cerr)
		}
		r, err := openPrivateLoreDB(rel, true)
		if err != nil {
			t.Fatalf("open %s read-only: %v", rel, err)
		}
		var n int
		if qerr := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&n); qerr != nil {
			t.Fatalf("read %s: %v", rel, qerr)
		}
		if cerr := r.Close(); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "client.db")); err != nil {
		t.Fatalf("../client.db did not land beside the repo: %v", err)
	}
}
