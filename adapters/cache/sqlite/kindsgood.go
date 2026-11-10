package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The last good definition of each resource kind (RES 3.13, OV35-LASTGOOD):
//
//	project(kinds | kind_fields row) ─► refreshKindGood(source, kind id)
//	   ├ kind row + its live field rows read from _rows
//	   ├ any doc holding conflict markers (merge.HasMarkers) → keep the
//	   │  old snapshot (the definition is not good now)
//	   └ else upsert _kinds_good(source, kind_id, def_sha, def_json, built_at)
//	LastGoodKind(source, kind id) ─► the snapshot's kind row + field rows
//
// The table is internal (not readable in lore sql) and survives Rebuild
// on purpose: a rebuild after a conflict must not forget the last good
// definition (3.13: "keeps using the last good parsed definition").
const tblKindsGood = "_kinds_good"

func init() {
	internalDDL = append(internalDDL,
		`CREATE TABLE IF NOT EXISTS "`+tblKindsGood+`" (source TEXT NOT NULL, kind_id TEXT NOT NULL, def_sha TEXT NOT NULL, def_json BLOB NOT NULL, built_at TEXT NOT NULL, PRIMARY KEY (source, kind_id))`)
}

// kindSnapshot is def_json: the kind row and its field rows as stored docs.
type kindSnapshot struct {
	Kind   json.RawMessage   `json:"kind"`
	Fields []json.RawMessage `json:"fields"`
}

// kindIDOfRow is the kind a kinds or kind_fields row defines ("" else).
func kindIDOfRow(r row.Row) string {
	switch r.Table {
	case schema.TableKinds:
		return r.ID.String()
	case schema.TableKindFields:
		if v, ok := r.Fields[fieldKindID]; ok {
			return v.String()
		}
	}
	return ""
}

const (
	kindDocQuery   = `SELECT doc FROM "` + tblRows + `" WHERE source = ? AND tbl = ? AND id = ? AND deleted = 0`
	kindFieldQuery = `SELECT doc FROM "` + tblRows + `" WHERE source = ? AND tbl = ? AND deleted = 0 AND json_valid(doc) AND json_extract(doc, '$.kind_id') = ? ORDER BY id`
	kindGoodUpsert = `INSERT INTO "` + tblKindsGood + `" (source, kind_id, def_sha, def_json, built_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (source, kind_id) DO UPDATE SET def_sha = excluded.def_sha, def_json = excluded.def_json, built_at = excluded.built_at
		WHERE def_sha <> excluded.def_sha`
	kindGoodRead = `SELECT def_json FROM "` + tblKindsGood + `" WHERE source = ? AND kind_id = ?`
)

// refreshKindsGood refreshes the snapshot of every kind an applied kinds
// or kind_fields row names (once per kind).
func refreshKindsGood(ctx context.Context, tx *sql.Tx, source store.Source, applied []store.Row) error {
	seen := map[string]bool{}
	for _, sr := range applied {
		if sr.Deleted || (sr.Table != schema.TableKinds && sr.Table != schema.TableKindFields) {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			continue // a newer _v: not a definition this binary reads
		}
		k := kindIDOfRow(r)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		if err := refreshKindGood(ctx, tx, source, k); err != nil {
			return err
		}
	}
	return nil
}

// refreshKindGood records kindID's definition in source as the last good
// one when neither the kind row nor any of its field rows holds conflict
// markers; a deleted or missing kind row leaves the snapshot alone.
func refreshKindGood(ctx context.Context, tx *sql.Tx, source store.Source, kindID string) error {
	if kindID == "" {
		return nil
	}
	var kdoc []byte
	switch err := tx.QueryRowContext(ctx, kindDocQuery, string(source), string(schema.TableKinds), kindID).Scan(&kdoc); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return dbErr("read kind row", err)
	}
	if docHasMarkers(kdoc) {
		return nil
	}
	snap := kindSnapshot{Kind: kdoc, Fields: []json.RawMessage{}}
	rs, err := tx.QueryContext(ctx, kindFieldQuery, string(source), string(schema.TableKindFields), kindID)
	if err != nil {
		return dbErr("read kind fields", err)
	}
	for rs.Next() {
		var d []byte
		if serr := rs.Scan(&d); serr != nil {
			return errors.Join(dbErr("read kind fields", serr), rs.Close())
		}
		if docHasMarkers(d) {
			return rs.Close() // not good now: keep the old snapshot
		}
		snap.Fields = append(snap.Fields, d)
	}
	if jerr := errors.Join(rs.Err(), rs.Close()); jerr != nil {
		return dbErr("read kind fields", jerr)
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return dbErr("encode kind snapshot", err)
	}
	sum := sha256.Sum256(b)
	if _, err := tx.ExecContext(ctx, kindGoodUpsert, string(source), kindID, hex.EncodeToString(sum[:]), b, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return dbErr("record last good kind", err)
	}
	return nil
}

// docHasMarkers reports a stored doc whose field values hold a conflict
// block. The doc is JSON, so a value's newlines are escaped there and
// merge.HasMarkers on the raw bytes never fires: each field is decoded and
// checked (the shape of ops/settings resources_lastgood). A doc this
// binary cannot decode counts as not good.
func docHasMarkers(doc []byte) bool {
	r, err := row.Decode(doc)
	if err != nil {
		return true
	}
	for _, v := range r.Fields {
		if v != nil && merge.HasMarkers(v.String()) {
			return true
		}
	}
	return false
}

// LastGoodKind returns the last good definition of kindID in source (RES
// 3.13): the kind row and its field rows as they were the last time none
// held conflict markers. ok is false for a kind never seen good here (3.13:
// such a kind loads nothing and its writes are refused).
func (c *Cache) LastGoodKind(ctx context.Context, source store.Source, kindID string) (kind row.Row, fields []row.Row, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if uerr := c.usable(); uerr != nil {
		return row.Row{}, nil, false, uerr
	}
	var raw []byte
	switch qerr := c.db.QueryRowContext(ctx, kindGoodRead, string(source), kindID).Scan(&raw); {
	case errors.Is(qerr, sql.ErrNoRows):
		return row.Row{}, nil, false, nil
	case qerr != nil:
		return row.Row{}, nil, false, dbErr("read last good kind", qerr)
	}
	var snap kindSnapshot
	if jerr := json.Unmarshal(raw, &snap); jerr != nil {
		return row.Row{}, nil, false, dbErr("decode last good kind", jerr)
	}
	if kind, err = row.Decode(snap.Kind); err != nil {
		return row.Row{}, nil, false, err
	}
	for _, f := range snap.Fields {
		fr, derr := row.Decode(f)
		if derr != nil {
			return row.Row{}, nil, false, derr
		}
		fields = append(fields, fr)
	}
	return kind, fields, true, nil
}
