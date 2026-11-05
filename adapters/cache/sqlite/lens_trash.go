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

import "context"

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
	// another row of its batch (links, comments, sub-tasks).
	Dependent bool
}

// viewTrash is the name `lore sql` reads the trash by (DELETION 2.9).
const viewTrash = "trash"

// createTrash creates and fills the trash table (columns in the order of
// DELETION 3.2, plus source and dependent).
func (l *Lens) createTrash(ctx context.Context) error {
	create := `CREATE TEMP TABLE "` + viewTrash + `" (source TEXT, table_name TEXT, id TEXT, title TEXT,
		deleted_at TEXT, deleted_by TEXT, deleted_reason TEXT, deleted_batch TEXT, dependent INTEGER)`
	if _, err := l.conn.ExecContext(ctx, create); err != nil {
		return dbErr("trash: create", err)
	}
	ins := `INSERT INTO "` + viewTrash + `" VALUES (?,?,?,?,?,?,?,?,?)`
	for _, t := range l.trash {
		dep := 0
		if t.Dependent {
			dep = 1
		}
		if _, err := l.conn.ExecContext(ctx, ins, t.Source, t.Table, t.ID, nullText(t.Title), nullText(t.DeletedAt),
			nullText(t.DeletedBy), nullText(t.Reason), nullText(t.Batch), dep); err != nil {
			return dbErr("trash: fill", err)
		}
	}
	l.views = append(l.views, viewTrash)
	return nil
}
