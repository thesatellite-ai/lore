package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/migrationlog"

	"github.com/thesatellite-ai/lore/model/errs"
)

// oldDB makes a lore.db shaped like one set up by an older release: the
// current schema minus actors.owner_actor_id (the 2026-11-04 incident
// column) and an applied log row for another definition.
func oldDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), ".lore")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "lore.db")
	if err := createProjectDB(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(dbPath)
	defer db.Close()
	for _, q := range []string{
		`ALTER TABLE actors DROP COLUMN owner_actor_id`,
		`UPDATE schema_migrations SET migration_sha256 = 'older'`,
		`INSERT INTO config(id, key, value, created_at, updated_at, setting_updated_at) VALUES ('cfg_x', 'schema_lore_version', '0.1.17', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return dbPath
}

func TestOpenDBFromPreviousRelease(t *testing.T) {
	ctx := context.Background()
	dbPath := oldDB(t)
	if err := ensureDBSchema(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(dbPath)
	defer db.Close()
	if missing, err := migrationlog.Drift(ctx, db); err != nil || len(missing) != 0 {
		t.Fatalf("drift after update: %v %v", missing, err)
	}
	if st, err := migrationlog.NeedsUpdate(ctx, db); err != nil || st != migrationlog.Current {
		t.Fatalf("state after update: %v %v", st, err)
	}
	if got := readSchemaVersion(ctx, db); got != version {
		t.Fatalf("stamp %q, want %q", got, version)
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(dbPath), "backups", "pre-schema-0.1.17-to-*.sqlite"))
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	// Second open: nothing to do, no second backup.
	if err := ensureDBSchema(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	backups, _ = filepath.Glob(filepath.Join(filepath.Dir(dbPath), "backups", "*.sqlite"))
	if len(backups) != 1 {
		t.Fatalf("second open made a backup: %v", backups)
	}
}

func TestOpenDBUpdateOffMapsToSchemaOld(t *testing.T) {
	ctx := context.Background()
	dbPath := oldDB(t)
	schemaAutoUpdate = false
	t.Cleanup(func() { schemaAutoUpdate = true })
	if err := ensureDBSchema(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(dbPath)
	defer db.Close()
	_, qerr := db.ExecContext(ctx, `SELECT owner_actor_id FROM actors`)
	if qerr == nil {
		t.Fatal("old DB has the column")
	}
	err := explainSchemaError(qerr)
	if !errcodes.IsCode(err, errcodes.Code(errs.SchemaOld)) {
		t.Fatalf("got %v, want E_SCHEMA_OLD", err)
	}
	lines, derr := schemaDriftLines(ctx, db)
	if derr != nil || len(lines) == 0 {
		t.Fatalf("doctor drift: %v %v", lines, derr)
	}
	if other := explainSchemaError(errors.New("boom")); other.Error() != "boom" {
		t.Fatalf("unrelated error changed: %v", other)
	}
}

func TestOpenDBNewerStampRefused(t *testing.T) {
	old := version
	version = "0.1.18"
	t.Cleanup(func() { version = old })
	if err := refuseNewerSchema("0.1.19"); !errcodes.IsCode(err, errcodes.SchemaVersionMismatch) {
		t.Fatalf("got %v", err)
	}
	for _, s := range []string{"", "0.1.17", "0.1.18"} {
		if err := refuseNewerSchema(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	version = "0.1.0-dev"
	if err := refuseNewerSchema("0.1.19"); err != nil {
		t.Fatalf("dev build refused: %v", err)
	}
}
