// schema_open.go — the lore.db schema is brought up to this binary on open
// (FORMAT_EVOLUTION 8.11, "Upgrade bug (found 2026-11-04)").
//
//	resolveContext ─▶ ensureDBSchema(dbPath)
//	                    ├─ NeedsUpdate == Current ─▶ return (one probe, two reads)
//	                    ├─ stamped by a newer lore ─▶ E_SCHEMA_VERSION_MISMATCH
//	                    ├─ read-only / hook off ─▶ return (a failing read maps to E_SCHEMA_OLD)
//	                    └─ lock .lore/state/schema.lock (wait) ─▶ re-check
//	                         ─▶ VACUUM INTO .lore/backups/pre-schema-<old>-to-<new>-<date>.sqlite
//	                         ─▶ runSetup (ent migrate + log + FTS) ─▶ stamp schema_lore_version
//	main ─▶ explainSchemaError: "no such column/table" ─▶ E_SCHEMA_OLD, hint `lore setup`
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/migrationlog"
	"saas/pkg/aicoder/processlock"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
)

// schemaVersionKey is the config row that names the lore_version that last
// set the schema up (8.11 part 1: the stamp carries the version next to the
// log's definition hash).
const schemaVersionKey = "schema_lore_version"

// schemaLockFile serialises the automatic update between lore processes on
// one database (8.9: a second process waits, re-reads, finds nothing to do).
const schemaLockFile = "schema.lock"

// schemaLockWait bounds the wait for another process's update.
const schemaLockWait = 60 * time.Second

// schemaLockPoll is how often a waiting process retries the lock.
const schemaLockPoll = 25 * time.Millisecond

// schemaBackupDir is the backups folder beside lore.db (the `lore backup`
// default, backup.go).
const schemaBackupDir = "backups"

// unknownSchemaVersion names the old side of a backup when the database
// carries no version stamp (every database set up before 0.1.18).
const unknownSchemaVersion = "old"

// schemaAutoUpdate is the test hook of TestOpenDBFromPreviousRelease's
// "update switched off" variant: false leaves an old database as it is, so
// the E_SCHEMA_OLD mapping can be checked. Always true in the binary.
var schemaAutoUpdate = true

// ensureDBSchema updates an older lore.db before the command touches it.
// A missing file is left to the caller (projresolve and materialize own it).
func ensureDBSchema(ctx context.Context, dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return nil // a missing DB is projresolve and materialize's case
	}
	state, stamped, err := schemaState(ctx, dbPath)
	if err != nil || state == migrationlog.Current {
		return err
	}
	if err := refuseNewerSchema(stamped); err != nil {
		return err
	}
	if !schemaAutoUpdate || os.Getenv("LORE_READ_ONLY") == envValueOn {
		return nil
	}
	unlock, err := lockSchema(dbPath)
	if err != nil {
		return err
	}
	defer unlock()
	// Another process may have updated it while this one waited.
	state, stamped, err = schemaState(ctx, dbPath)
	if err != nil || state == migrationlog.Current {
		return err
	}
	return updateSchema(ctx, dbPath, stamped)
}

// schemaState reads the log state and the version stamp on a short-lived
// connection (runSetup opens its own; ent's Migrate closes what it gets).
func schemaState(ctx context.Context, dbPath string) (migrationlog.State, string, error) {
	db := dbent.InitDB(dbPath)
	defer db.Close()
	state, err := migrationlog.NeedsUpdate(ctx, db)
	if err != nil {
		return state, "", errcodes.New(errcodes.Internal, "read schema migration log").WithCause(err)
	}
	if state == migrationlog.Current {
		return state, "", nil
	}
	return state, readSchemaVersion(ctx, db), nil
}

// readSchemaVersion is the stamp, "" when there is none (or no config table:
// a very old database).
func readSchemaVersion(ctx context.Context, db *sql.DB) string {
	var v string
	if err := db.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, schemaVersionKey).Scan(&v); err != nil {
		return ""
	}
	return v
}

// refuseNewerSchema: a database set up by a NEWER lore is never touched by
// this one (8.11 part 1, 8.9 downgrade row). A development build counts as
// the newest (it writes the format it knows, formatgate DECISION FMT-EFF).
func refuseNewerSchema(stamped string) error {
	if stamped == "" || isDevBuild(version) || !formatgate.VersionBelow(version, stamped) {
		return nil
	}
	return errcodes.New(errcodes.SchemaVersionMismatch,
		fmt.Sprintf("this database was set up by lore %s; this lore is %s and does not touch it", stamped, version)).
		WithHint("run `" + BinaryName + " upgrade`, or restore the pre-schema backup under .lore/backups")
}

// isDevBuild reports a source or snapshot build (FORMAT_EVOLUTION 8.1).
func isDevBuild(v string) bool {
	return strings.HasSuffix(v, "-dev") || strings.Contains(v, "snapshot")
}

// lockSchema takes .lore/state/schema.lock, waiting while another process
// updates the same database.
func lockSchema(dbPath string) (func(), error) {
	path := filepath.Join(filepath.Dir(dbPath), "state", schemaLockFile)
	deadline := time.Now().Add(schemaLockWait)
	for {
		l, err := processlock.Acquire(path)
		switch {
		case err == nil:
			return func() { _ = l.Release() }, nil // the OS drops the flock at exit anyway
		case errors.Is(err, processlock.ErrReadOnly):
			return func() {}, nil
		case !errors.Is(err, processlock.ErrLockHeld):
			return nil, errcodes.New(errcodes.Internal, "lock "+path).WithCause(err)
		}
		if time.Now().After(deadline) {
			return nil, errcodes.New(errcodes.DBLocked, "another lore process has been updating the database schema for over "+schemaLockWait.String())
		}
		time.Sleep(schemaLockPoll)
	}
}

// updateSchema backs the database up, then runs the schema update. A v0.1.17
// database was the write target and can hold rows not yet exported to
// files, so it is never thrown away unread: the backup comes first.
func updateSchema(ctx context.Context, dbPath, stamped string) error {
	old := stamped
	if old == "" {
		old = unknownSchemaVersion
	}
	backup, err := backupBeforeSchema(ctx, dbPath, old)
	if err != nil {
		return err
	}
	rel := backup
	if r, rerr := filepath.Rel(filepath.Dir(filepath.Dir(dbPath)), backup); rerr == nil {
		rel = r
	}
	fmt.Fprintf(os.Stderr, "%s: this database was set up by lore %s; updating it to %s (backup: %s)\n", BinaryName, old, version, rel)
	start := time.Now()
	if err := runSetup(ctx, dbPath, setupOptions{out: os.Stderr, quiet: true}); err != nil {
		return errcodes.New(errcodes.MigrationIncomplete, "the automatic database update did not finish").
			WithCause(err).
			WithHint("run `" + BinaryName + " setup`; the database before the update is " + rel)
	}
	fmt.Fprintf(os.Stderr, "%s: database updated in %s\n", BinaryName, time.Since(start).Round(time.Millisecond))
	return nil
}

// backupBeforeSchema writes .lore/backups/pre-schema-<old>-to-<new>-<date>.sqlite
// with VACUUM INTO (the `lore backup` mechanism). A name already taken today
// gets the time appended, so a second update never overwrites a backup.
func backupBeforeSchema(ctx context.Context, dbPath, old string) (string, error) {
	dir := filepath.Join(filepath.Dir(dbPath), schemaBackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errcodes.New(errcodes.Internal, "mkdir "+dir).WithCause(err)
	}
	now := time.Now().UTC()
	name := fmt.Sprintf("pre-schema-%s-to-%s-%s.sqlite", old, version, now.Format("2006-01-02"))
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		path = filepath.Join(dir, strings.TrimSuffix(name, ".sqlite")+"-"+now.Format("150405.000000000")+".sqlite")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "backup path").WithCause(err)
	}
	db := dbent.InitDB(dbPath)
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
		return "", errcodes.New(errcodes.Internal, "back the database up before its schema update").WithCause(err).
			WithHint("free disk space or run `" + BinaryName + " setup` after a manual `" + BinaryName + " backup`")
	}
	return abs, nil
}

// stampSchemaVersion records this binary as the one that set the schema up.
func stampSchemaVersion(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		INSERT INTO config(id, key, value, created_at, updated_at, setting_updated_at)
		VALUES ('cfg_' || lower(hex(randomblob(16))), ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP, setting_updated_at = CURRENT_TIMESTAMP
	`, schemaVersionKey, version); err != nil {
		return errcodes.New(errcodes.Internal, "stamp schema version").WithCause(err)
	}
	return nil
}

// sqliteMissingMarkers are SQLite's messages for a column or table the
// database does not have.
var sqliteMissingMarkers = []string{"no such column: ", "no such table: "}

// explainSchemaError maps a "no such column" / "no such table" SQL failure
// to E_SCHEMA_OLD with `lore setup` as the next step (8.11 part 4), instead
// of E_INTERNAL. It is the backstop for a database copied in from elsewhere
// or an interrupted update; any other error passes through unchanged.
func explainSchemaError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, m := range sqliteMissingMarkers {
		i := strings.Index(msg, m)
		if i < 0 {
			continue
		}
		what := msg[i:]
		if j := strings.IndexAny(what, "\n("); j > 0 {
			what = strings.TrimSpace(what[:j])
		}
		return errcodes.New(errcodes.Code(errs.SchemaOld),
			"the local database is older than this lore ("+what+")").
			WithCause(err).
			WithHint("run `" + BinaryName + " setup`")
	}
	return err
}

// schemaDriftLines is the doctor's view of 8.11 part 3: the real columns
// (PRAGMA table_info) against the schema this binary expects. Empty when
// nothing is missing.
func schemaDriftLines(ctx context.Context, db *sql.DB) ([]string, error) {
	missing, err := migrationlog.Drift(ctx, db)
	if err != nil || len(missing) == 0 {
		return nil, err
	}
	first := missing[0].String()
	kind := "column"
	if missing[0].Column == "" {
		kind = "table"
	}
	line := "the local database is older than this lore: missing " + kind + " " + first
	if n := len(missing) - 1; n > 0 {
		line += fmt.Sprintf(" (and %d more)", n)
	}
	return []string{line, style.Hint("run: " + BinaryName + " setup")}, nil
}
