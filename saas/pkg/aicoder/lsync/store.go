package lsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/schema/field"
)

// errUniqueViolation marks an import blocked by a natural-key unique index
// (two branches created "the same" tag/repo/actor with different ids).
var errUniqueViolation = errors.New("lsync: unique constraint")

// sqliteUniqueMsg is the message prefix SQLite uses for unique-index
// failures. The pure-Go driver exposes no typed constraint code, so the
// message is the stable contract (it has not changed across SQLite 3.x).
const sqliteUniqueMsg = "UNIQUE constraint failed"

// readRowDoc loads one row as a file document; (nil, nil) when absent.
func readRowDoc(ctx context.Context, q execer, t *Table, cols []Column, id string) (map[string]any, error) {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quoteIdent(c.Name)
	}
	row := q.QueryRowContext(ctx,
		`SELECT `+strings.Join(names, ", ")+` FROM `+quoteIdent(t.Name)+` WHERE id = ?`, id)
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lsync: read %s/%s: %w", t.Name, id, err)
	}
	return docFromRow(t, cols, vals)
}

// listRowIDs returns every id in a table.
func listRowIDs(ctx context.Context, q execer, t *Table) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM `+quoteIdent(t.Name)+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("lsync: list %s: %w", t.Name, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("lsync: scan %s id: %w", t.Name, err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// upsertRow writes a document's values into the DB.
//
// Insert path: every live column is bound; a NOT NULL column the document
// does not carry (an older file version, or a volatile column that never
// travels) gets its schema default or a type zero, so the insert cannot fail
// on a constraint the file could not know about.
//
// Update path (row exists): only columns PRESENT in the document are
// overwritten. Volatile columns therefore keep their local values, and a
// column an older file version lacks keeps its current DB value.
//
// Uses INSERT … ON CONFLICT(id) DO UPDATE (not INSERT OR REPLACE): REPLACE
// deletes then re-inserts, which would change the rowid that the FTS5
// triggers key search rows on.
func upsertRow(ctx context.Context, q execer, t *Table, cols, volatile []Column, ra rowArgs, now time.Time) error {
	var insCols, placeholders, updates []string
	var args []any
	for _, c := range cols {
		v, present := ra.values[c.Name]
		if !present {
			if c.Nullable {
				continue
			}
			v = zeroFor(c, now)
		} else if c.Name != idColumn {
			updates = append(updates, quoteIdent(c.Name)+" = excluded."+quoteIdent(c.Name))
		}
		insCols = append(insCols, quoteIdent(c.Name))
		placeholders = append(placeholders, "?")
		args = append(args, v)
	}
	for _, c := range volatile {
		insCols = append(insCols, quoteIdent(c.Name))
		placeholders = append(placeholders, "?")
		args = append(args, zeroFor(c, now))
	}
	stmt := `INSERT INTO ` + quoteIdent(t.Name) + ` (` + strings.Join(insCols, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`
	if len(updates) > 0 {
		stmt += ` ON CONFLICT(id) DO UPDATE SET ` + strings.Join(updates, ", ")
	} else {
		stmt += ` ON CONFLICT(id) DO NOTHING`
	}
	if _, err := q.ExecContext(ctx, stmt, args...); err != nil {
		// Only a unique index ON THIS TABLE is a natural-key collision; the
		// message names it as "<table>.<column>".
		if strings.Contains(err.Error(), sqliteUniqueMsg+": "+t.Name+".") {
			return fmt.Errorf("%w: %s: %v", errUniqueViolation, t.Name, err)
		}
		return fmt.Errorf("lsync: upsert %s: %w", t.Name, err)
	}
	return nil
}

// zeroFor returns the schema default, or a type-appropriate zero, for a
// NOT NULL column that has no value to bind.
func zeroFor(c Column, now time.Time) any {
	if c.Default != nil {
		return c.Default
	}
	switch c.Type {
	case field.TypeTime:
		return now.UTC()
	case field.TypeBool:
		return false
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		return int64(0)
	case field.TypeFloat32, field.TypeFloat64:
		return float64(0)
	case field.TypeJSON:
		return "null"
	default:
		return ""
	}
}

// deleteRowByID removes one row.
func deleteRowByID(ctx context.Context, q execer, t *Table, id string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM `+quoteIdent(t.Name)+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("lsync: delete %s/%s: %w", t.Name, id, err)
	}
	return nil
}

// findByNaturalKey returns the id of an existing row whose natural key
// equals the document's, other than the document's own id ("" if none).
func findByNaturalKey(ctx context.Context, q execer, t *Table, key []string, ra rowArgs) (string, error) {
	var where []string
	var args []any
	for _, k := range key {
		v, ok := ra.values[k]
		if !ok || v == nil {
			return "", nil // NULLs never collide in a SQLite unique index
		}
		where = append(where, quoteIdent(k)+" = ?")
		args = append(args, v)
	}
	args = append(args, ra.values[idColumn])
	var id string
	err := q.QueryRowContext(ctx,
		`SELECT id FROM `+quoteIdent(t.Name)+` WHERE `+strings.Join(where, " AND ")+` AND id <> ? LIMIT 1`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lsync: natural-key lookup %s: %w", t.Name, err)
	}
	return id, nil
}

// rewriteIDEverywhere replaces every occurrence of oldID with newID in every
// text column of every table (synced and local), so references survive a
// natural-key merge or a project-id adoption.
//
// lsync's own _lore_sync_* tables are excluded: they describe FILES on disk
// (a dirty mark for the loser id is what removes the loser's file) and
// history (trash and conflict copies must keep the ids rows had when they
// were set aside, so a restore puts back exactly what was removed).
//
// Why a blunt replace is safe: lore ids are "<prefix>_<32 hex>", globally
// unique random strings; a 36-character id cannot appear by accident inside
// unrelated text. Using replace() (not "= old") also fixes ids embedded in
// JSON columns (e.g. arrays of ids).
func rewriteIDEverywhere(ctx context.Context, q execer, oldID, newID string) error {
	rows, err := q.QueryContext(ctx, `SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE '%_fts%' AND m.name NOT LIKE 'sqlite_%'
		AND m.name NOT LIKE '\_lore\_sync\_%' ESCAPE '\'
		AND (upper(p.type) LIKE '%TEXT%' OR upper(p.type) LIKE '%CHAR%' OR p.type = '')`)
	if err != nil {
		return fmt.Errorf("lsync: list text columns: %w", err)
	}
	type tc struct{ table, col string }
	var targets []tc
	for rows.Next() {
		var x tc
		if err := rows.Scan(&x.table, &x.col); err != nil {
			rows.Close()
			return fmt.Errorf("lsync: scan text columns: %w", err)
		}
		targets = append(targets, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("lsync: list text columns: %w", err)
	}
	rows.Close()
	for _, x := range targets {
		col := quoteIdent(x.col)
		if _, err := q.ExecContext(ctx,
			`UPDATE `+quoteIdent(x.table)+` SET `+col+` = replace(`+col+`, ?, ?) WHERE instr(`+col+`, ?) > 0`,
			oldID, newID, oldID); err != nil {
			if !strings.Contains(err.Error(), sqliteUniqueMsg) {
				return fmt.Errorf("lsync: rewrite id in %s.%s: %w", x.table, x.col, err)
			}
			// Some rewritten rows collide with rows that already carry the
			// new id (e.g. an entity_tags binding both tag ids had, or the
			// loser row itself colliding on the primary key). Go row by
			// row: rewrite what can be rewritten, drop only the duplicates.
			if err := rewriteRowByRow(ctx, q, x.table, x.col, oldID, newID); err != nil {
				return err
			}
		}
	}
	return nil
}

// rewriteRowByRow is the slow path of rewriteIDEverywhere for a column whose
// bulk UPDATE hit a unique index.
func rewriteRowByRow(ctx context.Context, q execer, table, column, oldID, newID string) error {
	col := quoteIdent(column)
	rows, err := q.QueryContext(ctx, `SELECT rowid FROM `+quoteIdent(table)+` WHERE instr(`+col+`, ?) > 0`, oldID)
	if err != nil {
		return fmt.Errorf("lsync: list rows in %s: %w", table, err)
	}
	var rowids []int64
	for rows.Next() {
		var r int64
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return fmt.Errorf("lsync: scan rowid: %w", err)
		}
		rowids = append(rowids, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("lsync: list rows in %s: %w", table, err)
	}
	rows.Close()
	for _, r := range rowids {
		_, err := q.ExecContext(ctx, `UPDATE `+quoteIdent(table)+` SET `+col+` = replace(`+col+`, ?, ?) WHERE rowid = ?`, oldID, newID, r)
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), sqliteUniqueMsg) {
			return fmt.Errorf("lsync: rewrite id in %s.%s: %w", table, column, err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM `+quoteIdent(table)+` WHERE rowid = ?`, r); err != nil {
			return fmt.Errorf("lsync: drop duplicate in %s: %w", table, err)
		}
	}
	return nil
}
