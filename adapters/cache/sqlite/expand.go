package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
)

// Expander fills references (`${var:db_host}`, `${res:hub.ip}`) into text
// for row_expanded (spec 4.5). It is injected because resolving needs the
// engine's view of every store; the cache only records the result.
//
// Contract: Expand returns the text with every non-secret reference it can
// resolve filled in, and the ids of every thing the text references
// (resolved or not, secrets included, so a later change to any of them
// finds this row). A secret reference is NEVER expanded: it stays as
// written in expanded. An unresolvable reference is left as written, not an
// error; err is for real failures, and fails the Apply that asked.
type Expander interface {
	Expand(text string) (expanded string, refIDs []string, err error)
}

// Reexpand refreshes row_expanded for the rows that reference refID (a
// variable or resource that changed), found through ix_row_refs_ref; no
// other row is read or written. It returns how many fields were refreshed.
func (c *Cache) Reexpand(ctx context.Context, refID string) (int, error) {
	return c.ReexpandIDs(ctx, []string{refID})
}

// ReexpandIDs is Reexpand for several ids in one transaction (the rows a
// refresh applied).
func (c *Cache) ReexpandIDs(ctx context.Context, refIDs []string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return 0, err
	}
	n := 0
	var st state
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if st, err = c.current(ctx, tx); err != nil { // another handle may have changed the tables (devco:D-667)
			return err
		}
		targets, err := referrersOfAll(ctx, tx, refIDs)
		if err != nil {
			return err
		}
		for _, t := range targets {
			p, ok := st.plans[t.table]
			if !ok {
				continue
			}
			sel, ok := p.sql.selectText[t.field]
			if !ok {
				continue // not a text field of the current definition
			}
			var text sql.NullString
			err := txQueryRow(ctx, tx, sel, t.id).Scan(&text)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return dbErr("read referencing row", err)
			}
			if err := c.expandField(ctx, tx, t.table, t.id, t.field, text.String, false); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	c.st = st
	return n, nil
}

// refTarget is one (table, row, field) that references something.
type refTarget struct {
	table row.Table
	id    string
	field string
}

// referrersBatch is how many ids one referrersOfAll query asks for.
const referrersBatch = 500

// qReferrersOf lists the fields referencing any id of a JSON array of ids
// (ix_row_refs_ref through the IN list).
const qReferrersOf = `SELECT DISTINCT entity_table, entity_id, field FROM "` + tblRefs +
	`" WHERE ref_id IN (SELECT value FROM json_each(?)) ORDER BY 1, 2, 3`

// referrersOfAll lists the fields referencing any of refIDs, each field
// once, referrersBatch ids per query. Why (bug hunt H-282, devco:D-1020):
// a refresh re-expands for every row it applied, and one query per id
// (parsed again each time) was ~98 s of the first cache build of a
// 1.26M-row store.
func referrersOfAll(ctx context.Context, tx *sql.Tx, refIDs []string) ([]refTarget, error) {
	var out []refTarget
	seen := map[refTarget]bool{}
	for chunk := range slices.Chunk(refIDs, referrersBatch) {
		arr, err := json.Marshal(chunk)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "sqlite cache: encode ids", err)
		}
		got, err := scanReferrers(txQuery(ctx, tx, qReferrersOf, string(arr)))
		if err != nil {
			return nil, err
		}
		for _, t := range got {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// scanReferrers reads the (entity_table, entity_id, field) rows of a
// referrers query and closes them. Table and field names come back from
// the database, so callers validate them against the current plans
// before using them in SQL.
func scanReferrers(rs *sql.Rows, err error) ([]refTarget, error) {
	if err != nil {
		return nil, dbErr("find referencing rows", err)
	}
	defer func() { _ = rs.Close() }()
	var out []refTarget
	for rs.Next() {
		var t refTarget
		var table string
		if err := rs.Scan(&table, &t.id, &t.field); err != nil {
			return nil, dbErr("find referencing rows", err)
		}
		t.table = row.Table(table)
		out = append(out, t)
	}
	if err := rs.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "sqlite cache: find referencing rows", err)
	}
	return out, nil
}

// ReexpandMatching refreshes row_expanded for every text field that holds
// one of needles (e.g. "${var:db_host", the start of a reference to a name
// that just came into being or was renamed). Unresolved references have no
// row_refs line (SECRETS_AND_REFERENCES 8.7), so a variable or resource
// that is added later is found from the text instead; adds are rare, so
// the scan is cheap. It returns how many fields were refreshed.
func (c *Cache) ReexpandMatching(ctx context.Context, needles []string) (int, error) {
	if len(needles) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return 0, err
	}
	n := 0
	var st state
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if st, err = c.current(ctx, tx); err != nil { // another handle may have changed the tables (devco:D-667)
			return err
		}
		n, err = c.reexpandMatchingTx(ctx, tx, st, needles)
		return err
	})
	if err != nil {
		return 0, err
	}
	c.st = st
	return n, nil
}

// reexpandMatchingTx is ReexpandMatching inside tx over the tables of st.
func (c *Cache) reexpandMatchingTx(ctx context.Context, tx *sql.Tx, st state, needles []string) (int, error) {
	n := 0
	for _, p := range st.plans {
		tq, err := ident(p.def.Name.String())
		if err != nil {
			return 0, err
		}
		for _, f := range textFields(p) {
			fq, err := ident(f)
			if err != nil {
				return 0, err
			}
			for _, needle := range needles {
				got, err := c.reexpandWhere(ctx, tx, p, f,
					`SELECT "`+colID+`", `+fq+` FROM `+tq+` WHERE instr(`+fq+`, ?) > 0`, needle)
				if err != nil {
					return 0, err
				}
				n += got
			}
		}
	}
	return n, nil
}

// refStart is the start of every reference in a text field (`${var:…}`,
// `${res:…}`, `${row:…}`): RefreshRefs re-expands every field holding it.
const refStart = "${"

// RefreshRefs brings row_refs and row_expanded of this file up to stamp:
// the caller's reference rules and what they may name (wire: the refs
// version, bumped whenever what a reference resolves to changes, plus the
// epoch of the personal store's resources, which a write in another
// folder moves). The file keeps the stamp its references were last
// expanded at; when that is stamp nothing is read but that one row (the
// cheap check a read pays). Otherwise every text field holding a
// reference is expanded again and the stamp written, in one transaction.
// Why (devco D-793, owner P-12): rows expanded by an older binary, or
// before a resource they name arrived in another folder, kept their old
// row_refs (none) until a manual `lore search rebuild`, so a resource's
// mentions stayed missing. It returns how many fields were expanded again.
func (c *Cache) RefreshRefs(ctx context.Context, stamp string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return 0, err
	}
	if c.lazy != nil && c.lazy.Refs == stamp {
		return 0, nil // stamp.go: current, read from the open stamp
	}
	have, err := scanRefsStamp(c.db.QueryRowContext(ctx, qRefsStamp))
	if err != nil || have == stamp {
		return 0, err
	}
	n := 0
	var st state
	err = c.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if st, err = c.current(ctx, tx); err != nil {
			return err
		}
		if have, err := scanRefsStamp(txQueryRow(ctx, tx, qRefsStamp)); err != nil || have == stamp {
			return err // another process refreshed it meanwhile
		}
		if n, err = c.reexpandMatchingTx(ctx, tx, st, []string{refStart}); err != nil {
			return err
		}
		return writeRefsStamp(ctx, tx, stamp)
	})
	if err != nil {
		return 0, err
	}
	c.st = st
	return n, nil
}

// StampRefs records stamp as the file's refs stamp without expanding
// anything: the writer whose own Apply just re-expanded what a change
// touched (wire, devco D-793) keeps its file from expanding everything
// again on its next open.
func (c *Cache) StampRefs(ctx context.Context, stamp string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return err
	}
	return c.inTx(ctx, func(tx *sql.Tx) error { return writeRefsStamp(ctx, tx, stamp) })
}

// writeRefsStamp replaces the file's refs stamp inside tx.
func writeRefsStamp(ctx context.Context, tx *sql.Tx, stamp string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM "`+tblRefsStamp+`"`); err != nil {
		return dbErr("stamp refs", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO "`+tblRefsStamp+`" (stamp) VALUES (?)`, stamp); err != nil {
		return dbErr("stamp refs", err)
	}
	return nil
}

// qRefsStamp reads the refs stamp of a file: NULL when none (a file no
// binary with RefreshRefs has refreshed), read as "".
const qRefsStamp = `SELECT max(stamp) FROM "` + tblRefsStamp + `"`

// scanRefsStamp is the stamp qRefsStamp read.
func scanRefsStamp(r *sql.Row) (string, error) {
	var v sql.NullString
	if err := r.Scan(&v); err != nil {
		return "", dbErr("read refs stamp", err)
	}
	return v.String, nil
}

// reexpandWhere re-expands field f of the rows query (taking arg) returns
// as (id, text).
func (c *Cache) reexpandWhere(ctx context.Context, tx *sql.Tx, p plan, f, query, arg string) (int, error) {
	rs, err := txQuery(ctx, tx, query, arg)
	if err != nil {
		return 0, dbErr("find referencing text", err)
	}
	type hit struct{ id, text string }
	var hits []hit
	for rs.Next() {
		var h hit
		var t sql.NullString
		if err := rs.Scan(&h.id, &t); err != nil {
			_ = rs.Close()
			return 0, dbErr("find referencing text", err)
		}
		h.text = t.String
		hits = append(hits, h)
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		return 0, dbErr("find referencing text", err)
	}
	for _, h := range hits {
		if err := c.expandField(ctx, tx, p.def.Name, h.id, f, h.text, false); err != nil {
			return 0, err
		}
	}
	return len(hits), nil
}

// Referrer is one text field that references a row: row_refs (spec 4.5)
// read back. RefID is the referenced id the line holds.
type Referrer struct {
	Table row.Table
	ID    string
	Field string
	RefID string
}

// Referrers lists the fields whose text references one of refIDs (a
// resource a note mentions as ${res:…}, a ${row:…} target), sorted by
// table, id and field, each (table, id, field, ref) once. It reads
// row_refs through ix_row_refs_ref and writes nothing: resource graph --up
// and resource impact list the notes that mention a resource from it
// (devco D-764).
func (c *Cache) Referrers(ctx context.Context, refIDs []string) ([]Referrer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	var out []Referrer
	for _, refID := range refIDs {
		rs, err := c.db.QueryContext(ctx,
			`SELECT DISTINCT entity_table, entity_id, field FROM "`+tblRefs+`" WHERE ref_id = ? ORDER BY 1, 2, 3`, refID)
		if err != nil {
			return nil, dbErr("find referencing rows", err)
		}
		for rs.Next() {
			r := Referrer{RefID: refID}
			var table string
			if err := rs.Scan(&table, &r.ID, &r.Field); err != nil {
				return nil, errors.Join(dbErr("find referencing rows", err), rs.Close())
			}
			r.Table = row.Table(table)
			out = append(out, r)
		}
		if err := errors.Join(rs.Err(), rs.Close()); err != nil {
			return nil, dbErr("find referencing rows", err)
		}
	}
	return out, nil
}
