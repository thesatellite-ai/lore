package fts5

import (
	"context"
	"path/filepath"
	"testing"

	"dbent"
)

// Available runs on every command; it must detect FTS5 without touching the
// schema (a probe table changed it each time, so a concurrent lore process's
// integrity check failed with "database schema has changed").
func TestAvailableDoesNotChangeSchema(t *testing.T) {
	t.Parallel()
	// dbent.InitDB: the driver lore itself uses, at the workspace's version.
	db := dbent.InitDB(filepath.Join(t.TempDir(), "x.db"))
	defer func() { _ = db.Close() }() // test DB; nothing to flush
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (x)`); err != nil {
		t.Fatal(err)
	}
	version := func() int {
		var v int
		if err := db.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := version()
	if !Available(ctx, db) {
		t.Fatal("this build has FTS5")
	}
	if after := version(); after != before {
		t.Fatalf("Available changed the schema: version %d → %d", before, after)
	}
}
