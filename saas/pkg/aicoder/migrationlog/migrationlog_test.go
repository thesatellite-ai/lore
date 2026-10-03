package migrationlog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbent"
	"dbent/pkg/dbtemplate"
)

var templateDB string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "migrationlog-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir) // temp fixture
	templateDB = filepath.Join(dir, "t.db")
	if err := dbtemplate.Build(context.Background(), templateDB); err != nil {
		return 1
	}
	return m.Run()
}

func TestRecordCheckAndInterrupted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	defer db.Close()

	f, err := Check(ctx, db)
	if err != nil || len(f) != 1 || f[0].Fatal || !strings.Contains(f[0].Message, "no schema migration recorded") {
		t.Fatalf("empty log: %+v %v", f, err)
	}
	if err := Record(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, db); err != nil { // idempotent
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE status = 'applied'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d %v", n, err)
	}
	if f, _ := Check(ctx, db); len(f) != 0 {
		t.Fatalf("clean log reported: %+v", f)
	}
	if v, err := Begin(ctx, db); err != nil || v != 0 {
		t.Fatalf("nothing to migrate must return 0: %d %v", v, err)
	}

	// Tampered definition hash.
	if _, err := db.Exec(`UPDATE schema_migrations SET migration_sha256 = 'tampered'`); err != nil {
		t.Fatal(err)
	}
	f, _ = Check(ctx, db)
	if len(f) != 1 || !strings.Contains(f[0].Message, "hash mismatch") {
		t.Fatalf("tamper not reported: %+v", f)
	}
	// Interrupted migration (Begin without Complete).
	v, err := Begin(ctx, db)
	if err != nil || v != 2 {
		t.Fatalf("begin = %d %v", v, err)
	}
	f, _ = Check(ctx, db)
	if len(f) == 0 || !f[0].Fatal || !strings.Contains(f[0].Message, "in_progress") {
		t.Fatalf("interruption not reported: %+v", f)
	}
	if err := Complete(ctx, db, v); err != nil {
		t.Fatal(err)
	}
	if f, _ := Check(ctx, db); len(f) != 0 {
		t.Fatalf("after completion: %+v", f)
	}
}

func TestDefinitionHashStable(t *testing.T) {
	t.Parallel()
	first, second := DefinitionHash(), DefinitionHash()
	if first != second || len(first) != 64 {
		t.Fatal("definition hash must be deterministic sha256")
	}
}
