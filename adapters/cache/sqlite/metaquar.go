package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/logic/metafilter"
	"github.com/thesatellite-ai/lore/store"
)

// Quarantined row metadata in the cache (METADATA 3.13, OV14):
//
//	Apply ─► per applied row: refreshMetaQuarantine
//	   ├ tombstone, no meta, or meta that passes metafilter.Quarantine
//	   │  → no _meta_quarantine row
//	   └ problems → _meta_quarantine(source, tbl, id, problems)
//	row_meta (rowmeta.go) leaves quarantined rows out (3.13 "kept out")
//	MetaQuarantined(ctx) ─► every quarantined row, for lore sync status,
//	   lore doctor and lore meta lint (3.13 "counted")
//
// Problems name keys and rules, never values (metafilter.Problem), so the
// table can be read without masking.
const tblMetaQuarantine = "_meta_quarantine"

func init() {
	internalDDL = append(internalDDL,
		`CREATE TABLE IF NOT EXISTS "`+tblMetaQuarantine+`" (source TEXT NOT NULL, tbl TEXT NOT NULL, id TEXT NOT NULL, problems TEXT NOT NULL, PRIMARY KEY (source, tbl, id))`)
}

// MetaQuarantine is one row whose metadata is quarantined.
type MetaQuarantine struct {
	Source   store.Source
	Table    string
	ID       string
	Problems []string
}

const (
	metaQuarDelete = `DELETE FROM "` + tblMetaQuarantine + `" WHERE source = ? AND tbl = ? AND id = ?`
	metaQuarInsert = `INSERT OR REPLACE INTO "` + tblMetaQuarantine + `" (source, tbl, id, problems) VALUES (?, ?, ?, ?)`
	metaQuarList   = `SELECT source, tbl, id, problems FROM "` + tblMetaQuarantine + `" ORDER BY source, tbl, id`
	// problemSep joins problems in the column (a problem text never holds it).
	problemSep = "\n"
)

// refreshMetaQuarantine records (or clears) the quarantine of applied rows.
func refreshMetaQuarantine(ctx context.Context, tx *sql.Tx, source store.Source, applied []store.Row) error {
	for _, sr := range applied {
		if _, err := tx.ExecContext(ctx, metaQuarDelete, string(source), sr.Table.String(), sr.ID.String()); err != nil {
			return dbErr("clear meta quarantine", err)
		}
		if sr.Deleted {
			continue
		}
		r, err := sr.Decode()
		if err != nil || len(r.Meta) == 0 {
			continue
		}
		probs := metafilter.Quarantine(r.Meta)
		if len(probs) == 0 {
			continue
		}
		texts := make([]string, 0, len(probs))
		for _, p := range probs {
			texts = append(texts, problemText(p))
		}
		if _, err := tx.ExecContext(ctx, metaQuarInsert, string(source), sr.Table.String(), sr.ID.String(), strings.Join(texts, problemSep)); err != nil {
			return dbErr("record meta quarantine", err)
		}
	}
	return nil
}

// problemText is one stored problem: an entry's problem names its key id
// first ("key mky_…: the entry has no v"), so a reader of lore sync status
// or lore doctor knows which entry to fix or unset even when the entry has
// no readable name; a whole-row problem (entry count, size) stands alone.
func problemText(p metafilter.Problem) string {
	if p.Key == "" {
		return p.Text
	}
	return "key " + p.Key + ": " + p.Text
}

// MetaQuarantined lists every row whose metadata is quarantined, in every
// source of this file.
func (c *Cache) MetaQuarantined(ctx context.Context) ([]MetaQuarantine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	rs, err := c.db.QueryContext(ctx, metaQuarList)
	if err != nil {
		return nil, dbErr("read meta quarantine", err)
	}
	var out []MetaQuarantine
	for rs.Next() {
		var src, tbl, id, probs string
		if serr := rs.Scan(&src, &tbl, &id, &probs); serr != nil {
			return nil, errors.Join(dbErr("read meta quarantine", serr), rs.Close())
		}
		out = append(out, MetaQuarantine{Source: store.Source(src), Table: tbl, ID: id, Problems: strings.Split(probs, problemSep)})
	}
	if jerr := errors.Join(rs.Err(), rs.Close()); jerr != nil {
		return nil, dbErr("read meta quarantine", jerr)
	}
	return out, nil
}
