package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCacheDamagedIsMovedAsideAndRebuilt: a damaged repo cache (garbage
// in place of .lore/lore-cache.db, no WAL) no longer breaks every command:
// the next command moves it to .lore/backups/, says so once on stderr,
// rebuilds from .lore/data and answers with every row.
func TestCacheDamagedIsMovedAsideAndRebuilt(t *testing.T) {
	f := newCoreFixture(t)
	id := f.addID("memory", "add", "--body", "survives a damaged cache")
	cache := filepath.Join(f.dir, ".lore", "lore-cache.db")
	for _, s := range []string{"-wal", "-shm"} {
		if err := os.Remove(cache + s); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cache, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.run(e2eBin, "memory", "list")
	if r.code != 0 || !strings.Contains(r.stdout, id) || strings.Count(r.stderr, "was damaged; moved it to") != 1 {
		t.Fatalf("a damaged cache must be rebuilt with one note: exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	kept, _ := filepath.Glob(filepath.Join(f.dir, ".lore", "backups", "lore-cache.db.corrupt-*"))
	if len(kept) != 1 {
		t.Fatalf("the damaged file is kept in .lore/backups, got %v", kept)
	}
}

// TestCacheFinding: doctor names a cache damaged past its first pages
// (it opens, only an integrity check sees it) with the fix; a sound file
// and an absent one are no finding.
func TestCacheFinding(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "c.db")
	if got := cacheFinding(ctx, path); got != "" {
		t.Fatalf("absent: %q", got)
	}
	db, err := sql.Open(doctorCacheDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE t(a TEXT)", "CREATE INDEX ta ON t(a)",
		"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 3000) INSERT INTO t SELECT hex(randomblob(40)) FROM n"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := cacheFinding(ctx, path); got != "" {
		t.Fatalf("sound: %q", got)
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	for i := 8192; i < len(b)-4096; i++ { // keep page 1 (the header) and the tail
		b[i] = 0x5a
	}
	if err := os.WriteFile(filepath.Clean(path), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cacheFinding(ctx, path); !strings.Contains(got, "is damaged; delete it") {
		t.Fatalf("damaged: %q", got)
	}
}
