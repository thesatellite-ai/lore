package sqlite

// The `trash` view of `lore sql` (DELETION 3.2): one row per row in the
// trash of every store the lens reads. The cache drops a trashed row (it
// holds the hidden tombstone form), so the rows come in through
// LensOptions.Trash, read by the caller from the row files, and land in a
// TEMP table named trash before the lens locks.
//
//	wire App.SQL ─► engine.Trashed ─► []TrashRow (sensitive titles masked)
//	  └► OpenLensWith ─► setup ─► createTrash: CREATE TEMP TABLE trash, fill
//
// `SELECT id, deleted_by, deleted_reason FROM trash` (2.9). A query cannot
// write it: the lens is query_only once setup ends.

import (
	"context"
	"slices"
	"strings"
)

// TrashRow is one row in the trash as the `trash` view lists it. Every
// field is text; DeletedAt is RFC3339 UTC ("" when the row has none).
type TrashRow struct {
	Source    string // the store's name (the `source` column of the _all views)
	Table     string // table_name
	ID        string
	Title     string // the row's label; the caller masks a sensitive one
	DeletedAt string
	DeletedBy string // act_ id
	Reason    string // deleted_reason
	Batch     string // deleted_batch (bat_ id)
	// Dependent: the row went into the trash as a cascade dependent of
	// another row of its batch (a cascade relation: sub-tasks, a mission's
	// tasks; comments and links are never trashed with their target).
	Dependent bool
}

// viewTrash is the name `lore sql` reads the trash by (DELETION 2.9).
const viewTrash = "trash"

// ViewTrash is viewTrash for a caller outside the package (a test that
// queries the table by name).
const ViewTrash = viewTrash

// createTrash creates and fills the trash table (columns in the order of
// DELETION 3.2, plus source and dependent).
func (l *Lens) createTrash(ctx context.Context) error {
	create := `CREATE TEMP TABLE "` + viewTrash + `" (source TEXT, table_name TEXT, id TEXT, title TEXT,
		deleted_at TEXT, deleted_by TEXT, deleted_reason TEXT, deleted_batch TEXT, dependent INTEGER)`
	if _, err := l.conn.ExecContext(ctx, create); err != nil {
		return dbErr("trash: create", err)
	}
	if err := l.fillTrash(ctx, l.trash); err != nil {
		return err
	}
	l.views = append(l.views, viewTrash)
	return nil
}

// fillTrash inserts rows into the trash table.
func (l *Lens) fillTrash(ctx context.Context, rows []TrashRow) error {
	ins := `INSERT INTO "` + viewTrash + `" VALUES (?,?,?,?,?,?,?,?,?)`
	for _, t := range rows {
		dep := 0
		if t.Dependent {
			dep = 1
		}
		if _, err := l.conn.ExecContext(ctx, ins, t.Source, t.Table, t.ID, nullText(t.Title), nullText(t.DeletedAt),
			nullText(t.DeletedBy), nullText(t.Reason), nullText(t.Batch), dep); err != nil {
			return dbErr("trash: fill", err)
		}
	}
	return nil
}

// loadTrashFor fills the trash table from LensOptions.LoadTrash when the
// lens's query can read it (D-805). It runs after every view exists, so
// trashReaders sees them all, and before the lens locks. The loader's
// error is returned as it is: it already carries the caller's code.
func (l *Lens) loadTrashFor(ctx context.Context) error {
	if l.loadTrash == nil {
		return nil
	}
	readers, err := l.trashReaders(ctx)
	if err != nil {
		return err
	}
	if !namesAny(l.query, readers) {
		return nil
	}
	rows, err := l.loadTrash(ctx)
	if err != nil {
		return err
	}
	l.trash = append(l.trash, rows...)
	return l.fillTrash(ctx, rows)
}

// trashReaders is every name a query can read the trash table through:
// the table itself and each view of the lens whose SQL names the table or
// another such view (a fixed point over the view definitions, so a view
// over time_spent counts too). Derived from the schema, not listed: a new
// view built on the trash needs no change here (D-805: H-47 filled the
// trash only for a query naming it, and time_spent.task_deleted_at read
// NULL under --include-deleted).
func (l *Lens) trashReaders(ctx context.Context) ([]string, error) {
	rs, err := l.conn.QueryContext(ctx, `SELECT name, sql FROM sqlite_temp_master WHERE type = 'view'
		UNION ALL SELECT name, sql FROM main.sqlite_master WHERE type = 'view'`)
	if err != nil {
		return nil, dbErr("trash: list views", err)
	}
	defs := map[string][]string{} // view name → the name tokens of its SQL
	for rs.Next() {
		var name, body string
		if err = rs.Scan(&name, &body); err != nil {
			_ = rs.Close()
			return nil, dbErr("trash: list views", err)
		}
		defs[name] = nameTokens(body)
	}
	err = rs.Err()
	_ = rs.Close()
	if err != nil {
		return nil, dbErr("trash: list views", err)
	}
	readers := []string{viewTrash}
	for grew := true; grew; {
		grew = false
		for name, toks := range defs {
			if slices.ContainsFunc(readers, func(r string) bool { return strings.EqualFold(r, name) }) || !tokensName(toks, readers) {
				continue
			}
			readers = append(readers, name)
			grew = true
		}
	}
	slices.Sort(readers)
	return readers, nil
}

// namesAny reports whether the SQL text q names any of names, in any
// spelling SQLite accepts for a name (bare, "quoted", `ticked`,
// [bracketed], any case, schema-qualified): nameTokens unquotes each.
// A string literal holding a name counts too, which only costs a read.
func namesAny(q string, names []string) bool { return tokensName(nameTokens(q), names) }

// tokensName reports whether one of toks is one of names (any case).
func tokensName(toks, names []string) bool {
	for _, t := range toks {
		for _, n := range names {
			if strings.EqualFold(t, n) {
				return true
			}
		}
	}
	return false
}
