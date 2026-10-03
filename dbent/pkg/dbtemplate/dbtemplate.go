// Package dbtemplate builds one fully migrated SQLite file and copies it,
// so tests can create many databases without running the ent migrator more
// than once per process.
//
// Why it exists: ent's migrator writes to the package-level table metadata
// (dbent/gen/ent/migrate.Tables) while it runs. Two migrations in parallel
// tests — or a migration racing code that reads that metadata, like the
// lsync registry — is a data race under `go test -race`. The CLI is not
// affected (one migration per process); tests are. Build the template in
// TestMain, before any test goroutine starts, then Copy per test.
package dbtemplate

import (
	"context"
	"fmt"
	"io"
	"os"

	"dbent"
	"dbent/pkg/dbent_migrate"
)

// Build creates a migrated, checkpointed (single-file, no WAL sidecar)
// database at path.
func Build(ctx context.Context, path string) (err error) {
	db := dbent.InitDB(path)
	if err := dbent.ApplyPragmas(db); err != nil {
		_ = db.Close() // the pragma error is the one to report
		return fmt.Errorf("dbtemplate: pragmas: %w", err)
	}
	// Migrate closes the handle it is given.
	if err := dbent_migrate.Migrate(ctx, db); err != nil {
		return fmt.Errorf("dbtemplate: migrate: %w", err)
	}
	db = dbent.InitDB(path)
	defer func() {
		// Copy relies on the checkpoint below being complete on disk.
		if cerr := db.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("dbtemplate: close: %w", cerr)
		}
	}()
	// Fold the WAL into the main file so a plain file copy is complete.
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("dbtemplate: checkpoint: %w", err)
	}
	return nil
}

// Copy copies the template database file to dst.
func Copy(template, dst string) (err error) {
	in, err := os.Open(template)
	if err != nil {
		return fmt.Errorf("dbtemplate: open template: %w", err)
	}
	defer func() { _ = in.Close() }() // read-only: nothing to flush
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("dbtemplate: create copy: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("dbtemplate: close copy: %w", cerr)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("dbtemplate: copy: %w", err)
	}
	return nil
}
