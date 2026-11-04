// Package migrationlog keeps the schema_migrations replay log (R21 #49):
// one row per schema change applied to a lore.db, with a hash of the schema
// definition the binary migrated to and a hash of the resulting DB schema.
//
// Why: `lore setup` / `lore init` run ent's auto-migration. If that is
// killed half-way (power loss, SIGKILL), the DB is in an unknown shape and
// later commands fail in confusing ways; an `in_progress` row makes the
// interruption visible to `lore doctor`. A recorded definition hash that no
// longer matches the binary means either "this DB needs `lore setup`" or
// "someone edited the log" — doctor reports both.
//
// Rows are written with raw SQL so recording never depends on the ent
// client whose migration is being recorded.
package migrationlog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"dbent/gen/ent/migrate"

	"saas/pkg/aicoder/ids"
)

// Status values of schema_migrations.status (mirrors the ent enum).
const (
	StatusApplied    = "applied"
	StatusInProgress = "in_progress"
)

// table is the replay-log table created by the ent schema.
const table = "schema_migrations"

// DefinitionHash hashes the schema this binary migrates to: every table,
// column (name, type, nullability, uniqueness) and index of the generated
// ent schema, in a stable order. It changes exactly when the schema does.
func DefinitionHash() string {
	h := sha256.New()
	tables := append([]*schemaTable(nil), toTables()...)
	sort.Slice(tables, func(i, j int) bool { return tables[i].name < tables[j].name })
	for _, t := range tables {
		fmt.Fprintf(h, "table %s\n", t.name)
		for _, c := range t.cols {
			fmt.Fprintf(h, "  col %s\n", c)
		}
		for _, ix := range t.indexes {
			fmt.Fprintf(h, "  idx %s\n", ix)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

type schemaTable struct {
	name    string
	cols    []string
	indexes []string
}

func toTables() []*schemaTable {
	out := make([]*schemaTable, 0, len(migrate.Tables))
	for _, t := range migrate.Tables {
		st := &schemaTable{name: t.Name}
		for _, c := range t.Columns {
			st.cols = append(st.cols, fmt.Sprintf("%s %s null=%t unique=%t", c.Name, c.Type, c.Nullable, c.Unique))
		}
		for _, ix := range t.Indexes {
			var cols []string
			for _, c := range ix.Columns {
				cols = append(cols, c.Name)
			}
			st.indexes = append(st.indexes, fmt.Sprintf("%s unique=%t (%s)", ix.Name, ix.Unique, strings.Join(cols, ",")))
		}
		out = append(out, st)
	}
	return out
}

// DBSchemaHash hashes the schema SQL of every table and index in db, as
// SQLite stores it.
func DBSchemaHash(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, name, COALESCE(sql, '') FROM sqlite_master
		WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		return "", fmt.Errorf("migrationlog: read schema: %w", err)
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var typ, name, sqlText string
		if err := rows.Scan(&typ, &name, &sqlText); err != nil {
			return "", fmt.Errorf("migrationlog: scan schema: %w", err)
		}
		fmt.Fprintf(h, "%s %s %s\n", typ, name, sqlText)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Begin records an in_progress migration to the current definition and
// returns its version, or (0, nil) when the latest applied row already
// matches (nothing to migrate) or the log table does not exist yet (a
// brand-new DB: Record after the first migration instead).
func Begin(ctx context.Context, db *sql.DB) (int, error) {
	if ok, err := tableExists(ctx, db); err != nil || !ok {
		return 0, err
	}
	latest, err := latestApplied(ctx, db)
	if err != nil {
		return 0, err
	}
	def := DefinitionHash()
	// An interrupted migration is resumed, not stacked: the newest
	// in_progress row is re-pointed at this definition and returned, and
	// Complete closes it together with any older interrupted row. Without
	// this the open-time check (NeedsUpdate) would see the old row forever
	// and update the schema on every command (FORMAT_EVOLUTION 8.11).
	var pending sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM `+table+` WHERE status = ?`, StatusInProgress).Scan(&pending); err != nil {
		return 0, fmt.Errorf("migrationlog: in_progress: %w", err)
	}
	if pending.Valid {
		v := int(pending.Int64)
		if _, err := db.ExecContext(ctx, `UPDATE `+table+` SET migration_sha256 = ?, updated_at = ? WHERE version = ?`, def, time.Now().UTC(), v); err != nil {
			return 0, fmt.Errorf("migrationlog: resume v%d: %w", v, err)
		}
		return v, nil
	}
	if latest != nil && latest.migrationHash == def {
		return 0, nil
	}
	version := 1
	if latest != nil {
		version = latest.version + 1
	}
	if err := insert(ctx, db, version, def, "-", StatusInProgress); err != nil {
		return 0, err
	}
	return version, nil
}

// Complete marks version applied and stores the resulting DB schema hash.
// A zero version (Begin found nothing to do) is a no-op.
func Complete(ctx context.Context, db *sql.DB, version int) error {
	if version == 0 {
		return nil
	}
	schemaHash, err := DBSchemaHash(ctx, db)
	if err != nil {
		return err
	}
	// version <= closes older interrupted rows too (Begin resumed them).
	if _, err := db.ExecContext(ctx, `UPDATE `+table+` SET status = ?, schema_sha256 = ?, updated_at = ? WHERE version <= ? AND (version = ? OR status = ?)`,
		StatusApplied, schemaHash, time.Now().UTC(), version, version, StatusInProgress); err != nil {
		return fmt.Errorf("migrationlog: complete v%d: %w", version, err)
	}
	return nil
}

// Record writes an applied row for the current definition when the latest
// applied row does not already match. Used right after the very first
// migration of a new DB (the log table did not exist before it).
func Record(ctx context.Context, db *sql.DB) error {
	v, err := Begin(ctx, db)
	if err != nil {
		return err
	}
	return Complete(ctx, db, v)
}

// Finding is one problem Check reports.
type Finding struct {
	// Fatal: the DB shape is unknown (an interrupted migration).
	Fatal   bool
	Message string
}

// Check inspects the log: interrupted migrations (fatal), a latest applied
// definition hash that differs from this binary's (setup needed, or the log
// was edited), and an empty log on an existing DB (informational).
func Check(ctx context.Context, db *sql.DB) ([]Finding, error) {
	ok, err := tableExists(ctx, db)
	if err != nil || !ok {
		return nil, err
	}
	var out []Finding
	rows, err := db.QueryContext(ctx, `SELECT version FROM `+table+` WHERE status = ? ORDER BY version`, StatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("migrationlog: check: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, Finding{Fatal: true, Message: fmt.Sprintf(
			"E_MIGRATION_INCOMPLETE: schema migration v%d is in_progress (interrupted); run `lore setup` to finish it, or `lore repair --tier=2` to restore a backup", v)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	latest, err := latestApplied(ctx, db)
	if err != nil {
		return nil, err
	}
	switch {
	case latest == nil:
		out = append(out, Finding{Message: "no schema migration recorded; run `lore setup` to record one"})
	case latest.migrationHash != DefinitionHash():
		out = append(out, Finding{Message: fmt.Sprintf(
			"schema migration hash mismatch for v%d (recorded %s, this lore expects %s): run `lore setup`; if the DB is already current, the migration log was tampered",
			latest.version, short(latest.migrationHash), short(DefinitionHash()))})
	}
	return out, nil
}

// shortHashLen is how much of a hash Check prints.
const shortHashLen = 12

func short(h string) string {
	if len(h) > shortHashLen {
		return h[:shortHashLen]
	}
	return h
}

type applied struct {
	version       int
	migrationHash string
}

func latestApplied(ctx context.Context, db *sql.DB) (*applied, error) {
	var a applied
	err := db.QueryRowContext(ctx, `SELECT version, migration_sha256 FROM `+table+` WHERE status = ? ORDER BY version DESC LIMIT 1`, StatusApplied).
		Scan(&a.version, &a.migrationHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrationlog: latest: %w", err)
	}
	return &a, nil
}

func insert(ctx context.Context, db *sql.DB, version int, migrationHash, schemaHash, status string) error {
	id, err := ids.New(ids.PrefixSchemaMigration)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO `+table+`(id, created_at, updated_at, version, migration_sha256, schema_sha256, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, now, now, version, migrationHash, schemaHash, status); err != nil {
		return fmt.Errorf("migrationlog: record v%d: %w", version, err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
		return false, fmt.Errorf("migrationlog: probe: %w", err)
	}
	return n == 1, nil
}

// State is what NeedsUpdate found (FORMAT_EVOLUTION 8.11 part 1).
type State int

const (
	// Current means the latest applied row matches this binary's definition and
	// no migration is in progress; nothing to do on open.
	Current State = iota
	// Behind means no log, no applied row, or an applied row for an older
	// definition; the schema is updated on open (backup first).
	Behind
	// Incomplete means a migration is in_progress (interrupted); the update is
	// re-run on open, which finishes it.
	Incomplete
)

// NeedsUpdate is the per-open check: one probe of sqlite_master plus two
// indexed reads of the log. It never writes.
func NeedsUpdate(ctx context.Context, db *sql.DB) (State, error) {
	ok, err := tableExists(ctx, db)
	if err != nil {
		return Current, err
	}
	if !ok {
		return Behind, nil
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE status = ?`, StatusInProgress).Scan(&n); err != nil {
		return Current, fmt.Errorf("migrationlog: in_progress: %w", err)
	}
	if n > 0 {
		return Incomplete, nil
	}
	latest, err := latestApplied(ctx, db)
	if err != nil {
		return Current, err
	}
	if latest == nil || latest.migrationHash != DefinitionHash() {
		return Behind, nil
	}
	return Current, nil
}

// Missing is one table or column this binary expects that the DB lacks.
// Column is "" for a missing table.
type Missing struct {
	Table  string
	Column string
}

// String is "table" or "table.column", the form doctor and E_SCHEMA_OLD print.
func (m Missing) String() string {
	if m.Column == "" {
		return m.Table
	}
	return m.Table + "." + m.Column
}

// Drift compares the real columns (PRAGMA table_info per table) with the
// schema this binary expects (toTables) and lists what is missing, in table
// order (FORMAT_EVOLUTION 8.11 part 3: doctor reads the real shape, never
// only the log). Extra tables and columns are not drift: a newer lore or a
// plugin may add them.
func Drift(ctx context.Context, db *sql.DB) ([]Missing, error) {
	tables := append([]*schemaTable(nil), toTables()...)
	sort.Slice(tables, func(i, j int) bool { return tables[i].name < tables[j].name })
	var out []Missing
	for _, t := range tables {
		have, err := columnsOf(ctx, db, t.name)
		if err != nil {
			return nil, err
		}
		if len(have) == 0 {
			out = append(out, Missing{Table: t.name})
			continue
		}
		for _, c := range t.cols {
			name, _, _ := strings.Cut(c, " ")
			if !have[name] {
				out = append(out, Missing{Table: t.name, Column: name})
			}
		}
	}
	return out, nil
}

// columnsOf is the column set of table (empty when it does not exist).
// The name comes from the compiled schema, never from input;
// pragma_table_info takes it as a bound parameter.
func columnsOf(ctx context.Context, db *sql.DB, name string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, name)
	if err != nil {
		return nil, fmt.Errorf("migrationlog: table_info %s: %w", name, err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		have[c] = true
	}
	return have, rows.Err()
}
