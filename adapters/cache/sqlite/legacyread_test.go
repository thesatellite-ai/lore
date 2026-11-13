package sqlite

// legacyread_test.go — F-8: a read of a lore 0.1.17 lore.db (WAL mode)
// in a format-1 CI clone left .lore/lore.db-wal and -shm in the work tree.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// walDB makes a WAL-mode database at path with one table and closes it
// (the last close folds the WAL in and removes the sidecars).
func walDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open(driverName, fileuri.URL(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE tasks (id TEXT)", "INSERT INTO tasks VALUES ('t1')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyReadLeavesNoSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lore.db")
	walDB(t, path)
	for _, s := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + s); err == nil {
			t.Fatalf("setup left %s", s)
		}
	}
	if _, err := ReadLegacyAuditLog(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + s); err == nil {
			t.Errorf("a read of the old lore.db created %s beside it", s)
		}
	}
}

func TestLegacyReadQueryKeepsAWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lore.db")
	if q := LegacyReadQuery(path, false, ""); !strings.Contains(q, "immutable=1") {
		t.Errorf("no -wal: %q is not immutable", q)
	}
	if err := os.WriteFile(path+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// a -wal may hold rows not in the main file yet: read through it
	if q := LegacyReadQuery(path, false, "a=b"); q != "mode=ro&a=b" {
		t.Errorf("with a -wal: %q", q)
	}
	if q := LegacyReadQuery(path, true, ""); q != "mode=ro&immutable=1" {
		t.Errorf("frozen: %q", q)
	}
}
