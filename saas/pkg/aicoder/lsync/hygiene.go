package lsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// idValuePattern matches a lore id inside a column value.
var idValuePattern = regexp.MustCompile(`^[a-z]{3}_[0-9a-f]{32}$`)

// refColumnSuffix marks reference columns (<thing>_id). Polymorphic pairs
// (entity_table + entity_id) follow the same naming.
const refColumnSuffix = "_id"

// DanglingRef is a reference to a synced row that does not exist — normal
// right after merging branches that disagree (E8): the CLI shows the
// reference as missing instead of failing, and doctor lists it.
type DanglingRef struct {
	Table  string `json:"table"`
	RowID  string `json:"row_id"`
	Column string `json:"column"`
	Target string `json:"target"`
}

// DanglingReferences lists *_id values in synced tables that name an id
// with a synced-table prefix but match no row. Ids whose prefix belongs to
// a local table (e.g. a code_file) are ignored: those rows are per-machine
// by design.
func DanglingReferences(ctx context.Context, db *sql.DB) ([]DanglingRef, error) {
	reg, _ := NewRegistry()
	present, err := existingColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	syncedPrefixes := map[string]bool{}
	for _, name := range reg.Names() {
		if present[name] == nil {
			continue
		}
		t, _ := reg.Table(name)
		idsList, err := listRowIDs(ctx, db, t)
		if err != nil {
			return nil, err
		}
		for _, id := range idsList {
			existing[id] = true
			syncedPrefixes[id[:3]] = true
		}
	}
	var out []DanglingRef
	for _, name := range reg.Names() {
		t, _ := reg.Table(name)
		for _, c := range liveColumns(t, present) {
			if c.Name == idColumn || !strings.HasSuffix(c.Name, refColumnSuffix) {
				continue
			}
			rows, err := db.QueryContext(ctx, `SELECT id, `+quoteIdent(c.Name)+` FROM `+quoteIdent(name)+` WHERE `+quoteIdent(c.Name)+` IS NOT NULL`)
			if err != nil {
				return nil, fmt.Errorf("lsync: scan %s.%s: %w", name, c.Name, err)
			}
			for rows.Next() {
				var id, ref string
				if err := rows.Scan(&id, &ref); err != nil {
					rows.Close()
					return nil, fmt.Errorf("lsync: scan ref: %w", err)
				}
				if idValuePattern.MatchString(ref) && syncedPrefixes[ref[:3]] && !existing[ref] {
					out = append(out, DanglingRef{Table: name, RowID: id, Column: c.Name, Target: ref})
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	return out, nil
}

// duplicateTextColumns are the columns whose normalised text identifies
// "the same knowledge written twice" (E48), in priority order.
var duplicateTextColumns = []string{"body", "title"}

// DuplicateGroup is a set of rows in one table with identical normalised
// text — typically the same fact recorded independently on two clones.
type DuplicateGroup struct {
	Table string   `json:"table"`
	IDs   []string `json:"ids"`
	Text  string   `json:"text"`
}

// FindDuplicates groups non-archived rows of each synced table by
// whitespace- and case-normalised body (or title). Exact matches only:
// "similar" is a judgement call left to a human or an agent, who merges with
// MergeRows.
func FindDuplicates(ctx context.Context, db *sql.DB) ([]DuplicateGroup, error) {
	reg, _ := NewRegistry()
	present, err := existingColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []DuplicateGroup
	for _, name := range reg.Names() {
		col := ""
		for _, c := range duplicateTextColumns {
			if present[name][c] {
				col = c
				break
			}
		}
		if col == "" {
			continue
		}
		where := ""
		if present[name][archivedAtColumn] {
			where = ` WHERE archived_at IS NULL`
		}
		rows, err := db.QueryContext(ctx, `SELECT id, `+quoteIdent(col)+` FROM `+quoteIdent(name)+where)
		if err != nil {
			return nil, fmt.Errorf("lsync: scan %s: %w", name, err)
		}
		groups := map[string][]string{}
		first := map[string]string{}
		for rows.Next() {
			var id string
			var text sql.NullString
			if err := rows.Scan(&id, &text); err != nil {
				rows.Close()
				return nil, err
			}
			key := normaliseText(text.String)
			if key == "" {
				continue
			}
			groups[key] = append(groups[key], id)
			if _, ok := first[key]; !ok {
				first[key] = text.String
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		for key, idsList := range groups {
			if len(idsList) > 1 {
				sort.Strings(idsList)
				out = append(out, DuplicateGroup{Table: name, IDs: idsList, Text: first[key]})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].IDs[0] < out[j].IDs[0]
	})
	return out, nil
}

func normaliseText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// MergeRows folds row drop into row keep (same table): every reference to
// drop is rewritten to keep, drop goes to the trash and is deleted. The
// next Reconcile exports the rewritten rows and removes drop's file.
func MergeRows(ctx context.Context, db *sql.DB, table, keep, drop string) error {
	if keep == drop {
		return fmt.Errorf("lsync: keep and drop are the same row")
	}
	reg, _ := NewRegistry()
	t, ok := reg.Table(table)
	if !ok {
		return fmt.Errorf("lsync: %s is not a synced table", table)
	}
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return err
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lsync: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	cols := liveColumns(t, present)
	for _, id := range []string{keep, drop} {
		doc, err := readRowDoc(ctx, tx, t, cols, id)
		if err != nil {
			return err
		}
		if doc == nil {
			return fmt.Errorf("%w: %s/%s", ErrNotFound, table, id)
		}
	}
	e := &engine{reg: reg, present: present, tx: tx, now: time.Now().UTC(), rep: &Report{}, blocked: map[string]bool{}, deferred: map[rowKey]bool{}}
	if err := e.deleteRowToTrash(ctx, rowKey{table, drop}, t, "merged into "+keep); err != nil {
		return err
	}
	if err := rewriteIDEverywhere(ctx, tx, drop, keep); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+tblDirty+`(table_name, row_id) VALUES (?, ?)`, table, drop); err != nil {
		return fmt.Errorf("lsync: mark dropped row: %w", err)
	}
	return tx.Commit()
}

// DecodeRowFile parses a row file's bytes into a document (used to show
// rows read straight from another git ref without importing them).
func DecodeRowFile(b []byte) (map[string]any, error) {
	return decodeDoc(b)
}

// CheckFiles validates every row file in dataDir without touching the DB:
// it parses each file and applies the same checks the importer does
// (format version, _table/id match the path, no conflict markers). Used by
// `lore doctor`, which must not depend on a sync pass having run.
func CheckFiles(dataDir string) ([]FileError, error) {
	reg, _ := NewRegistry()
	scan, err := scanDataDir(dataDir, reg, time.Now())
	if err != nil {
		return nil, err
	}
	var out []FileError
	for rel := range scan.files {
		table, id, _ := parseRelPath(rel)
		b, err := readRowFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil {
			out = append(out, FileError{Path: rel, Kind: ErrKindInvalid, Detail: err.Error()})
			continue
		}
		if kind, detail := validateRowFile(b, table, id); kind != "" {
			out = append(out, FileError{Path: rel, Kind: kind, Detail: detail})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// UnexportedChange is a synced row whose DB state differs from what lore
// last synced, and that no pass has picked up yet.
type UnexportedChange struct {
	Table string `json:"table"`
	ID    string `json:"id"`
}

// UnexportedChanges lists rows changed in lore.db since the last sync pass
// (the change triggers marked them dirty) whose content really differs from
// the synced base — e.g. an edit made with sqlite3. Lore's own writes are
// exported at the end of the command that made them, so right after any
// lore command this is empty unless something else wrote to the DB. Rows
// whose export was refused for a credential pattern are excluded (lore made
// them; they are reported as sync errors instead).
//
// Read-only: must be called WITHOUT running a pass first, or the pass
// would export (and thereby accept) the very changes being looked for.
func UnexportedChanges(ctx context.Context, db *sql.DB) ([]UnexportedChange, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return nil, err
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT d.table_name, d.row_id FROM `+tblDirty+` d
		WHERE NOT EXISTS (SELECT 1 FROM `+tblErrors+` e WHERE e.path = d.table_name || '/' || d.row_id || '.json' AND e.kind = ?)
		ORDER BY d.table_name, d.row_id`, ErrKindSecret)
	if err != nil {
		return nil, fmt.Errorf("lsync: list dirty: %w", err)
	}
	var keys []rowKey
	for rows.Next() {
		var k rowKey
		if err := rows.Scan(&k.table, &k.id); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var out []UnexportedChange
	for _, k := range keys {
		t, ok := reg.Table(k.table)
		if !ok {
			continue
		}
		dbDoc, err := readRowDoc(ctx, db, t, liveColumns(t, present), k.id)
		if err != nil {
			return nil, err
		}
		var base []byte
		err = db.QueryRowContext(ctx, `SELECT base FROM `+tblFiles+` WHERE path = ?`, k.rel()).Scan(&base)
		var baseDoc map[string]any
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return nil, err
		default:
			if baseDoc, err = decodeDoc(base); err != nil {
				baseDoc = nil
			}
		}
		same, err := sameDoc(dbDoc, baseDoc)
		if err != nil {
			return nil, err
		}
		if !same {
			out = append(out, UnexportedChange{Table: k.table, ID: k.id})
		}
	}
	return out, nil
}
