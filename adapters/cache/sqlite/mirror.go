package sqlite

import (
	"context"
	"database/sql"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// tblMirror holds store.RowMirror entries: one decoded row per (source,
// row file path), valid only for the blob id it was read under.
const tblMirror = "_mirror"

// mirrorDDL creates tblMirror (idempotent; part of internalDDL).
var mirrorDDL = `CREATE TABLE IF NOT EXISTS "` + tblMirror + `" (source TEXT NOT NULL, path TEXT NOT NULL, blob TEXT NOT NULL, tbl TEXT NOT NULL, id TEXT NOT NULL, hlc TEXT NOT NULL, deleted INTEGER NOT NULL, doc BLOB NOT NULL, PRIMARY KEY (source, path))`

// mirrorTableIndexDDL indexes tblMirror by table: Lookup reads one
// table's entries, and on the (source, path) key alone each lookup read
// every entry of the source with its document (8 MB in a 5,500-row repo,
// every command: owner's Mac warm task list 4 s, D-110).
var mirrorTableIndexDDL = `CREATE INDEX IF NOT EXISTS "` + tblMirror + `_tbl" ON "` + tblMirror + `" (source, tbl)`

// errMirrorBusy is the mirror's answer while the cache is in use.
var errMirrorBusy = errs.New(errs.Internal, "sqlite cache: busy, the row mirror is skipped")

// Mirror implements store.MirrorCache.
func (c *Cache) Mirror(source store.Source) store.RowMirror { return mirror{c: c, source: source} }

// mirror is one source's store.RowMirror in c. It never waits for the
// cache: a read can run inside a cache operation (Apply expands references
// by listing rows, holding c.mu), so while c is busy Lookup answers
// errMirrorBusy (the files are read instead) and Keep keeps nothing.
type mirror struct {
	c      *Cache
	source store.Source
}

// Lookup implements store.RowMirror.
func (m mirror) Lookup(ctx context.Context, table row.Table, want map[string]string) (map[string]store.Row, error) {
	if !m.c.mu.TryLock() {
		return nil, errMirrorBusy
	}
	defer m.c.mu.Unlock()
	if err := m.c.usable(); err != nil {
		return nil, err
	}
	rs, err := m.c.db.QueryContext(ctx, `SELECT path, blob, id, hlc, deleted, doc FROM "`+tblMirror+`" WHERE source = ? AND tbl = ?`, string(m.source), table.String())
	if err != nil {
		return nil, dbErr("read the row mirror", err)
	}
	defer rs.Close()
	out := map[string]store.Row{}
	for rs.Next() {
		var path, blob, id, stamp string
		var deleted int
		var doc []byte
		if err := rs.Scan(&path, &blob, &id, &stamp, &deleted, &doc); err != nil {
			return nil, dbErr("read the row mirror", err)
		}
		if want[path] != blob || blob == "" {
			continue
		}
		rid, err := ids.ParseAny(id)
		if err != nil {
			continue // not lore's: the file is read instead
		}
		h, err := hlc.Parse(stamp)
		if err != nil {
			continue
		}
		out[path] = store.Row{Table: table, ID: rid, HLC: h, Deleted: deleted != 0, Doc: doc}
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("read the row mirror", err)
	}
	return out, nil
}

// Keep implements store.RowMirror.
func (m mirror) Keep(ctx context.Context, entries []store.MirrorEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if !m.c.mu.TryLock() {
		return errMirrorBusy
	}
	defer m.c.mu.Unlock()
	if err := m.c.usable(); err != nil {
		return err
	}
	return m.c.inTx(ctx, func(tx *sql.Tx) error {
		for _, e := range entries {
			deleted := 0
			if e.Row.Deleted {
				deleted = 1
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO "`+tblMirror+`" (source, path, blob, tbl, id, hlc, deleted, doc) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				string(m.source), e.Path, e.Blob, e.Row.Table.String(), e.Row.ID.String(), e.Row.HLC.String(), deleted, e.Row.Doc); err != nil {
				return dbErr("keep the row mirror", err)
			}
		}
		return nil
	})
}
