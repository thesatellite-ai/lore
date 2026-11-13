package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestReopenLeavesTheFileAlone (B1-16): opening an existing cache changes
// nothing in it. The row_meta view was dropped and created again on every
// open (and the layout version set again), so every command, a read
// included, wrote the cache file.
func TestReopenLeavesTheFileAlone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "c.db")
	version := func() int {
		t.Helper()
		db, err := sql.Open(driverName, "file:"+path+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		var v int
		if err := db.QueryRowContext(f.ctx, "PRAGMA schema_version").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	reopen := func() {
		t.Helper()
		if err := f.open("c.db").Close(); err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	before := version()
	reopen()
	reopen()
	if after := version(); after != before {
		t.Fatalf("reopening changed the schema: version %d → %d", before, after)
	}
	c := f.open("c.db") // the view still answers
	if n := count(t, c, `SELECT COUNT(*) FROM "`+ViewRowMeta+`"`); n != 0 {
		t.Fatalf("row_meta of an empty cache has %d rows", n)
	}
}
