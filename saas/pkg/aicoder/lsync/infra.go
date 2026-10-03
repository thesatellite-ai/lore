package lsync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// Bookkeeping tables. They live in lore.db next to the ent tables, are
// created with raw SQL (like the FTS5 tables in saas/pkg/aicoder/fts5) and
// are never synced. The leading underscore keeps them out of ent's way:
// ent's auto-migration only creates/alters the tables it knows and never
// drops unknown ones.
const (
	tblMeta      = "_lore_sync_meta"
	tblFiles     = "_lore_sync_files"
	tblDirty     = "_lore_sync_dirty"
	tblErrors    = "_lore_sync_errors"
	tblConflicts = "_lore_sync_conflicts"
	tblTrash     = "_lore_sync_trash"
	tblExtra     = "_lore_sync_extra"
)

// triggerPrefix names every change-capture trigger so they can be listed and
// dropped as a group when the synced column set changes.
const triggerPrefix = "_lore_sync_trg_"

// Meta keys stored in _lore_sync_meta.
const (
	// metaTriggerSig is a hash of the synced (table, columns) set the
	// installed triggers were generated from; a mismatch regenerates them.
	metaTriggerSig = "trigger_sig"
	// metaBaselined is "1" once this DB has been bootstrapped or adopted;
	// until then Reconcile runs BOOTSTRAP or ADOPT instead of a normal pass.
	metaBaselined = "baselined"
	// metaFlagSet is the value a boolean meta key holds when set (absent =
	// unset). Writers and readers both use it, so they cannot disagree.
	metaFlagSet = "1"
	// metaAdoptPrefer, when set, decides ADOPT clashes (a PreferMode value)
	// instead of newest-wins. Written by `lore restore --prefer`.
	metaAdoptPrefer = "adopt_prefer"
	// metaImportedAt records the last time a pass imported file changes
	// (RFC3339Nano). metaRenderedImport holds the metaImportedAt value the
	// last re-render of LORE.md covered; when they differ a re-render is
	// due — even if the import happened in a git hook, not in a command.
	metaImportedAt     = "imported_at"
	metaRenderedImport = "rendered_import_at"
)

// schemaStatements create the bookkeeping tables idempotently.
//
// Field notes:
//   - _lore_sync_files.base holds the canonical bytes last synced in either
//     direction. It is the merge BASE for the both-sides-changed case
//     (§12.2) — a hash alone cannot drive a field-level merge.
//   - size / mtime_ns / synced_at_ns implement git's racy-clean stat cache
//     so an unchanged file is never re-read.
//   - _lore_sync_dirty is a SET (primary key), not a log: it records "this
//     row may differ from its file"; the drain compares actual content.
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS ` + tblMeta + ` (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ` + tblFiles + ` (
		path TEXT PRIMARY KEY,
		table_name TEXT NOT NULL,
		row_id TEXT NOT NULL,
		hash TEXT NOT NULL,
		base BLOB NOT NULL,
		size INTEGER NOT NULL,
		mtime_ns INTEGER NOT NULL,
		synced_at_ns INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ` + tblDirty + ` (
		table_name TEXT NOT NULL,
		row_id TEXT NOT NULL,
		PRIMARY KEY (table_name, row_id))`,
	`CREATE TABLE IF NOT EXISTS ` + tblErrors + ` (
		path TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		detail TEXT NOT NULL,
		at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ` + tblConflicts + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		table_name TEXT NOT NULL,
		row_id TEXT NOT NULL,
		kept TEXT NOT NULL,
		other_doc TEXT NOT NULL,
		at TEXT NOT NULL,
		resolved INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS ` + tblTrash + ` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		table_name TEXT NOT NULL,
		row_id TEXT NOT NULL,
		doc TEXT NOT NULL,
		reason TEXT NOT NULL,
		at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ` + tblExtra + ` (path TEXT PRIMARY KEY, doc TEXT NOT NULL)`,
}

// EnsureInfra creates the bookkeeping tables and (re)installs the change
// triggers when the synced column set changed since they were generated.
//
// Why triggers (and not hooks in Go code): lore's own CLI writes through
// ent AND through raw SQL in a dozen files, and `lore tui`, sqlite3 by hand
// and scripts bypass the CLI entirely. A trigger is the one place that sees
// every write. They capture CHANGE only (a row id into a set); they enforce
// nothing, so they are not business validation in the database.
//
// Idempotent and cheap on the common path: one SELECT of the stored
// signature.
func EnsureInfra(ctx context.Context, db *sql.DB, reg *Registry) error {
	for _, s := range schemaStatements {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("lsync: ensure schema: %w", err)
		}
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return err
	}
	sig := triggerSignature(reg, present)
	cur, _, err := getMeta(ctx, db, metaTriggerSig)
	if err != nil {
		return err
	}
	if cur == sig {
		return nil
	}
	return installTriggers(ctx, db, reg, present, sig)
}

// existingColumns returns, per synced-candidate table, the columns that
// physically exist in this DB file. A DB migrated by an OLDER lore binary
// may lack a column the registry knows; triggers and SQL must only name
// columns that exist.
//
// Only synced tables are introspected: lore.db also holds ~100 FTS shadow
// and local tables, and this runs on every command.
func existingColumns(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	names := make([]string, 0, len(syncedTables))
	args := make([]any, 0, len(syncedTables))
	for n := range syncedTables {
		names = append(names, "?")
		args = append(args, n)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND m.name IN (`+strings.Join(names, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("lsync: list columns: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, fmt.Errorf("lsync: scan columns: %w", err)
		}
		if out[t] == nil {
			out[t] = map[string]bool{}
		}
		out[t][c] = true
	}
	return out, rows.Err()
}

// liveColumns returns the synced columns of t that exist in this DB.
func liveColumns(t *Table, present map[string]map[string]bool) []Column {
	var cols []Column
	for _, c := range t.Columns {
		if present[t.Name][c.Name] {
			cols = append(cols, c)
		}
	}
	return cols
}

func triggerSignature(reg *Registry, present map[string]map[string]bool) string {
	h := sha256.New()
	for _, n := range reg.Names() {
		t, _ := reg.Table(n)
		if present[n] == nil {
			continue
		}
		h.Write([]byte(n + ":"))
		for _, c := range liveColumns(t, present) {
			h.Write([]byte(c.Name + ","))
		}
		h.Write([]byte(";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// installTriggers drops every lsync trigger and recreates them for the
// current column set, atomically.
//
// UPDATE OF lists only synced columns, so updates that touch nothing but
// volatile columns (access counters) never mark a row dirty. An UPDATE that
// changes the id records both the old and the new id: the old one reads as
// "deleted" and the new one as "added" when drained.
func installTriggers(ctx context.Context, db *sql.DB, reg *Registry, present map[string]map[string]bool, sig string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lsync: begin trigger install: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE ? ESCAPE '\'`,
		strings.ReplaceAll(triggerPrefix, "_", `\_`)+"%")
	if err != nil {
		return fmt.Errorf("lsync: list triggers: %w", err)
	}
	var old []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return fmt.Errorf("lsync: scan trigger: %w", err)
		}
		old = append(old, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("lsync: list triggers: %w", err)
	}
	rows.Close()
	for _, n := range old {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+quoteIdent(n)); err != nil {
			return fmt.Errorf("lsync: drop trigger %s: %w", n, err)
		}
	}
	for _, n := range reg.Names() {
		if present[n] == nil {
			continue // table not migrated into this DB yet
		}
		t, _ := reg.Table(n)
		var colNames []string
		for _, c := range liveColumns(t, present) {
			colNames = append(colNames, quoteIdent(c.Name))
		}
		// NOT "INSERT OR IGNORE": SQLite lets the conflict policy of the
		// statement that FIRED the trigger override the one inside it, so
		// an outer UPSERT would turn a duplicate into a hard UNIQUE error.
		// A guarded plain INSERT never conflicts.
		ins := func(ref string) string {
			return `INSERT INTO ` + tblDirty + `(table_name, row_id) SELECT '` + n + `', ` + ref + `.id WHERE NOT EXISTS (SELECT 1 FROM ` +
				tblDirty + ` WHERE table_name = '` + n + `' AND row_id = ` + ref + `.id);`
		}
		stmts := []string{
			`CREATE TRIGGER ` + quoteIdent(triggerPrefix+n+"_ai") + ` AFTER INSERT ON ` + quoteIdent(n) + ` BEGIN ` + ins("NEW") + ` END`,
			`CREATE TRIGGER ` + quoteIdent(triggerPrefix+n+"_au") + ` AFTER UPDATE OF ` + strings.Join(colNames, ", ") + ` ON ` + quoteIdent(n) + ` BEGIN ` + ins("OLD") + ` ` + ins("NEW") + ` END`,
			`CREATE TRIGGER ` + quoteIdent(triggerPrefix+n+"_ad") + ` AFTER DELETE ON ` + quoteIdent(n) + ` BEGIN ` + ins("OLD") + ` END`,
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("lsync: create trigger on %s: %w", n, err)
			}
		}
	}
	if err := setMeta(ctx, tx, metaTriggerSig, sig); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lsync: commit triggers: %w", err)
	}
	return nil
}

// Execer is satisfied by *sql.DB and *sql.Tx: what OnChange receives so it
// can write in the pass's transaction.
type Execer = execer

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getMeta(ctx context.Context, q execer, key string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM `+tblMeta+` WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lsync: read meta %s: %w", key, err)
	}
	return v, true, nil
}

func setMeta(ctx context.Context, q execer, key, value string) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO `+tblMeta+`(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value); err != nil {
		return fmt.Errorf("lsync: write meta %s: %w", key, err)
	}
	return nil
}

// callerStatePrefix namespaces keys owned by lsync's callers (the CLI's
// git-wiring cache) so they can never collide with the engine's own keys.
const callerStatePrefix = "caller."

// GetState reads a caller-owned value stored in lore.db next to the sync
// bookkeeping ("" when unset). Use it for per-clone caches that must live
// with the DB, never in $HOME.
func GetState(ctx context.Context, db *sql.DB, key string) (string, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return "", err
	}
	v, _, err := getMeta(ctx, db, callerStatePrefix+key)
	return v, err
}

// SetState stores a caller-owned value (see GetState).
func SetState(ctx context.Context, db *sql.DB, key, value string) error {
	return setMeta(ctx, db, callerStatePrefix+key, value)
}

func deleteMeta(ctx context.Context, q execer, key string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM `+tblMeta+` WHERE key = ?`, key); err != nil {
		return fmt.Errorf("lsync: delete meta %s: %w", key, err)
	}
	return nil
}

// quoteIdent quotes an SQL identifier. Identifiers here come from the
// generated schema or our own constants, never from user input; quoting
// still protects against reserved words (e.g. a column named "key").
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
