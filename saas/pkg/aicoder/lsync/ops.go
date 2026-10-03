package lsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Status is a read-only snapshot of the sync state (`lore sync status`).
type Status struct {
	Baselined      bool        `json:"baselined"`
	DataDirPresent bool        `json:"data_dir_present"`
	TrackedFiles   int         `json:"tracked_files"`
	PendingExports int         `json:"pending_exports"`
	OpenConflicts  int         `json:"open_conflicts"`
	TrashRows      int         `json:"trash_rows"`
	Errors         []FileError `json:"errors,omitempty"`
}

// ReadStatus reports the sync state without changing anything.
func ReadStatus(ctx context.Context, db *sql.DB, dataDir string) (Status, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return Status{}, err
	}
	st := Status{DataDirPresent: MetaExists(dataDir)}
	b, _, err := getMeta(ctx, db, metaBaselined)
	if err != nil {
		return st, err
	}
	st.Baselined = b == metaFlagSet
	counts := []struct {
		dst *int
		sql string
	}{
		{&st.TrackedFiles, `SELECT COUNT(*) FROM ` + tblFiles},
		{&st.PendingExports, `SELECT COUNT(*) FROM ` + tblDirty},
		{&st.OpenConflicts, `SELECT COUNT(*) FROM ` + tblConflicts + ` WHERE resolved = 0`},
		{&st.TrashRows, `SELECT COUNT(*) FROM ` + tblTrash},
	}
	for _, c := range counts {
		if err := db.QueryRowContext(ctx, c.sql).Scan(c.dst); err != nil {
			return st, fmt.Errorf("lsync: status: %w", err)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT path, kind, detail FROM `+tblErrors+` ORDER BY path`)
	if err != nil {
		return st, fmt.Errorf("lsync: status errors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var fe FileError
		if err := rows.Scan(&fe.Path, &fe.Kind, &fe.Detail); err != nil {
			return st, fmt.Errorf("lsync: scan error row: %w", err)
		}
		st.Errors = append(st.Errors, fe)
	}
	return st, rows.Err()
}

// ConflictRecord is one saved conflict copy.
type ConflictRecord struct {
	ID       int64    `json:"id"`
	Table    string   `json:"table"`
	RowID    string   `json:"row_id"`
	Kept     KeptSide `json:"kept"`
	OtherDoc string   `json:"other_doc"`
	At       string   `json:"at"`
	Resolved bool     `json:"resolved"`
}

// ListConflicts returns saved conflict copies, open ones only unless all.
func ListConflicts(ctx context.Context, db *sql.DB, all bool) ([]ConflictRecord, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return nil, err
	}
	q := `SELECT id, table_name, row_id, kept, other_doc, at, resolved FROM ` + tblConflicts
	if !all {
		q += ` WHERE resolved = 0`
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("lsync: list conflicts: %w", err)
	}
	defer rows.Close()
	var out []ConflictRecord
	for rows.Next() {
		var c ConflictRecord
		var resolved int
		if err := rows.Scan(&c.ID, &c.Table, &c.RowID, &c.Kept, &c.OtherDoc, &c.At, &resolved); err != nil {
			return nil, fmt.Errorf("lsync: scan conflict: %w", err)
		}
		c.Resolved = resolved != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveTake selects which version ResolveConflict keeps.
type ResolveTake string

const (
	// TakeKept accepts the version the sync pass already kept.
	TakeKept ResolveTake = "kept"
	// TakeOther replaces the row with the saved conflict copy.
	TakeOther ResolveTake = "other"
)

// ErrNotFound is returned for an unknown conflict or trash id.
var ErrNotFound = errors.New("lsync: not found")

// ResolveConflict closes a conflict. With TakeOther the saved copy is
// written back into the DB; the change triggers export it on the next
// Reconcile, so the decision reaches git like any other edit.
func ResolveConflict(ctx context.Context, db *sql.DB, id int64, take ResolveTake) error {
	var c ConflictRecord
	err := db.QueryRowContext(ctx, `SELECT table_name, row_id, other_doc FROM `+tblConflicts+` WHERE id = ? AND resolved = 0`, id).
		Scan(&c.Table, &c.RowID, &c.OtherDoc)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: open conflict %d", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("lsync: load conflict: %w", err)
	}
	if take == TakeOther {
		if err := writeDocToDB(ctx, db, c.Table, c.OtherDoc); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+tblConflicts+` SET resolved = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("lsync: mark resolved: %w", err)
	}
	return nil
}

// TrashRecord is a row removed because its file disappeared.
type TrashRecord struct {
	ID     int64  `json:"id"`
	Table  string `json:"table"`
	RowID  string `json:"row_id"`
	Reason string `json:"reason"`
	At     string `json:"at"`
	Doc    string `json:"doc"`
}

// ListTrash returns trashed rows, newest first.
func ListTrash(ctx context.Context, db *sql.DB) ([]TrashRecord, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, table_name, row_id, reason, at, doc FROM `+tblTrash+` ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("lsync: list trash: %w", err)
	}
	defer rows.Close()
	var out []TrashRecord
	for rows.Next() {
		var r TrashRecord
		if err := rows.Scan(&r.ID, &r.Table, &r.RowID, &r.Reason, &r.At, &r.Doc); err != nil {
			return nil, fmt.Errorf("lsync: scan trash: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RestoreTrash puts a trashed row back. The next Reconcile exports it, which
// recreates its file — i.e. restoring is an ordinary, reviewable edit.
func RestoreTrash(ctx context.Context, db *sql.DB, id int64) error {
	var r TrashRecord
	err := db.QueryRowContext(ctx, `SELECT table_name, doc FROM `+tblTrash+` WHERE id = ?`, id).Scan(&r.Table, &r.Doc)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: trash entry %d", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("lsync: load trash: %w", err)
	}
	if err := writeDocToDB(ctx, db, r.Table, r.Doc); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM `+tblTrash+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("lsync: drop trash entry: %w", err)
	}
	return nil
}

// writeDocToDB upserts a stored document through the normal codec.
func writeDocToDB(ctx context.Context, db *sql.DB, table, docText string) error {
	reg, _ := NewRegistry()
	t, ok := reg.Table(table)
	if !ok {
		return fmt.Errorf("lsync: %s is not a synced table", table)
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return err
	}
	doc, err := decodeDoc([]byte(docText))
	if err != nil {
		return err
	}
	ra, err := argsFromDoc(t, doc)
	if err != nil {
		return err
	}
	return upsertRow(ctx, db, t, liveColumns(t, present), liveVolatile(t, present), ra, time.Now().UTC())
}

// ResetForRestore forgets every sync baseline in db so the next Reconcile
// runs ADOPT with the given preference. `lore restore` calls it on the
// restored file: without it the restored rows would compare against stale
// bases and silently lose to the files (E43).
func ResetForRestore(ctx context.Context, db *sql.DB, prefer PreferMode) error {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM ` + tblFiles,
		`DELETE FROM ` + tblDirty,
		`DELETE FROM ` + tblExtra,
		`DELETE FROM ` + tblErrors,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("lsync: reset: %w", err)
		}
	}
	if err := deleteMeta(ctx, db, metaBaselined); err != nil {
		return err
	}
	if prefer == PreferNewest {
		return deleteMeta(ctx, db, metaAdoptPrefer)
	}
	return setMeta(ctx, db, metaAdoptPrefer, string(prefer))
}

// MarkAllForExport makes the next Reconcile rewrite every row file from the
// DB (`lore sync export --all`): it recreates the data dir and _meta.json
// when missing, forgets the base index, and marks every synced row dirty.
func MarkAllForExport(ctx context.Context, db *sql.DB, dataDir string) error {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return err
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, dataDirMode); err != nil {
		return fmt.Errorf("lsync: create data dir: %w", err)
	}
	if !MetaExists(dataDir) {
		e := &engine{reg: reg, present: present}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("lsync: begin: %w", err)
		}
		e.tx = tx
		e.rep = &Report{}
		pid, err := e.primaryProjectID(ctx)
		_ = tx.Rollback() // read-only use of the tx
		if err != nil {
			return err
		}
		if err := writeProjectMeta(dataDir, ProjectMeta{ProjectID: pid}); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM `+tblFiles); err != nil {
		return fmt.Errorf("lsync: forget bases: %w", err)
	}
	for _, name := range reg.Names() {
		if present[name] == nil {
			continue
		}
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO `+tblDirty+`(table_name, row_id) SELECT ?, id FROM `+quoteIdent(name), name); err != nil {
			return fmt.Errorf("lsync: mark %s: %w", name, err)
		}
	}
	return setMeta(ctx, db, metaBaselined, metaFlagSet)
}

// RerenderDue reports whether file changes were imported since the last
// MarkRerendered — i.e. generated views of the data (LORE.md) are stale.
func RerenderDue(ctx context.Context, db *sql.DB) (bool, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return false, err
	}
	imported, ok, err := getMeta(ctx, db, metaImportedAt)
	if err != nil || !ok {
		return false, err
	}
	rendered, _, err := getMeta(ctx, db, metaRenderedImport)
	if err != nil {
		return false, err
	}
	return rendered != imported, nil
}

// MarkRerendered records that generated views now reflect every import.
func MarkRerendered(ctx context.Context, db *sql.DB) error {
	imported, ok, err := getMeta(ctx, db, metaImportedAt)
	if err != nil || !ok {
		return err
	}
	return setMeta(ctx, db, metaRenderedImport, imported)
}

// archivedAtColumn marks soft-deleted rows (LifecycleMixin and friends).
const archivedAtColumn = "archived_at"

// PurgeArchived permanently removes rows archived before cutoff: each goes
// to the trash, its id is appended to _purged.json (so an old clone's ADOPT
// never resurrects it, E47), and the next Reconcile removes its file.
// Returns the purged "table/id" keys.
func PurgeArchived(ctx context.Context, db *sql.DB, dataDir string, cutoff time.Time) ([]string, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return nil, err
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	purged, err := readPurged(dataDir)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("lsync: begin purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	e := &engine{reg: reg, present: present, tx: tx, now: time.Now().UTC(), rep: &Report{}, blocked: map[string]bool{}, deferred: map[rowKey]bool{}}
	keys, err := archivedBefore(ctx, tx, reg, present, cutoff)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys {
		t, _ := reg.Table(k.table)
		if err := e.deleteRowToTrash(ctx, k, t, "purged"); err != nil {
			return nil, err
		}
		// deleteRowToTrash clears the dirty flag; re-mark it so the next
		// Reconcile removes the file.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+tblDirty+`(table_name, row_id) VALUES (?, ?)`, k.table, k.id); err != nil {
			return nil, fmt.Errorf("lsync: mark purged: %w", err)
		}
		purged[k.id] = true
		out = append(out, k.table+"/"+k.id)
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("lsync: commit purge: %w", err)
	}
	return out, writePurged(dataDir, purged)
}

// CountArchivedBefore is the dry-run of PurgeArchived.
func CountArchivedBefore(ctx context.Context, db *sql.DB, cutoff time.Time) (int, error) {
	reg, _ := NewRegistry()
	present, err := existingColumns(ctx, db)
	if err != nil {
		return 0, err
	}
	keys, err := archivedBefore(ctx, db, reg, present, cutoff)
	return len(keys), err
}

// archivedBefore lists synced rows whose archived_at is before cutoff.
func archivedBefore(ctx context.Context, q execer, reg *Registry, present map[string]map[string]bool, cutoff time.Time) ([]rowKey, error) {
	var out []rowKey
	for _, name := range reg.Names() {
		t, _ := reg.Table(name)
		if !present[name][archivedAtColumn] {
			continue
		}
		idsList, err := listRowIDs(ctx, q, t)
		if err != nil {
			return nil, err
		}
		cols := liveColumns(t, present)
		for _, id := range idsList {
			doc, err := readRowDoc(ctx, q, t, cols, id)
			if err != nil {
				return nil, err
			}
			s, _ := doc[archivedAtColumn].(string)
			if s == "" {
				continue
			}
			at, err := parseLooseTime(s)
			if err != nil || !at.Before(cutoff) {
				continue
			}
			out = append(out, rowKey{name, id})
		}
	}
	return out, nil
}

// FixProjects collapses several project rows into one (E1, two parallel
// bootstraps that met in a merge). keep defaults to the id _meta.json pins,
// else the smallest id. Every reference is rewritten; the next Reconcile
// exports the rewritten rows and removes the other project files.
func FixProjects(ctx context.Context, db *sql.DB, dataDir, keep string) (string, []string, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return "", nil, err
	}
	t, _ := reg.Table(projectsTable)
	all, err := listRowIDs(ctx, db, t)
	if err != nil {
		return "", nil, err
	}
	if len(all) < 2 {
		return "", nil, nil
	}
	if keep == "" {
		if m, err := ReadProjectMeta(dataDir); err == nil && m.ProjectID != "" {
			keep = m.ProjectID
		} else {
			keep = all[0]
		}
	}
	found := false
	for _, id := range all {
		if id == keep {
			found = true
		}
	}
	if !found {
		return "", nil, fmt.Errorf("%w: project %s", ErrNotFound, keep)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("lsync: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	var merged []string
	for _, id := range all {
		if id == keep {
			continue
		}
		if err := rewriteIDEverywhere(ctx, tx, id, keep); err != nil {
			return "", nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+tblDirty+`(table_name, row_id) VALUES (?, ?)`, projectsTable, id); err != nil {
			return "", nil, fmt.Errorf("lsync: mark project: %w", err)
		}
		merged = append(merged, id)
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("lsync: commit: %w", err)
	}
	return keep, merged, writeProjectMeta(dataDir, ProjectMeta{ProjectID: keep})
}
