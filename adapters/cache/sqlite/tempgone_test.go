package sqlite

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestCacheOpensWhenTempFolderIsGone: with the temp folder (TMPDIR, or
// TMP/TEMP on Windows) naming a folder that does not exist, SQLite's own
// temp files have nowhere to go; on Windows creating the fts tables failed
// with "unable to open database file". The cache then keeps them in memory
// (temp_store=MEMORY, 2), so it still opens and answers.
func TestCacheOpensWhenTempFolderIsGone(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "removed-temp")
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, gone)
	}
	if runtime.GOOS != "windows" && tempUsable() {
		t.Fatalf("temp folder %s should be unusable", gone)
	}
	f := newFixture(t)
	c := f.open("cache.db")
	var mode int
	if err := c.db.QueryRow("PRAGMA temp_store").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("temp_store = %d with the temp folder gone, want 2 (MEMORY)", mode)
	}
}
