package sqlite

import (
	"context"
	"database/sql"
	"errors"

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
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		var targets []refTarget
		for _, refID := range refIDs {
			got, err := referrers(ctx, tx, refID)
			if err != nil {
				return err
			}
			targets = append(targets, got...)
		}
		for _, t := range targets {
			p, ok := c.st.plans[t.table]
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
			if err := c.expandField(ctx, tx, t.table, t.id, t.field, text.String); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// refTarget is one (table, row, field) that references something.
type refTarget struct {
	table row.Table
	id    string
	field string
}

// referrers lists the fields referencing refID. Table and field names come
// back from the database, so callers validate them against the current
// plans before using them in SQL.
func referrers(ctx context.Context, tx *sql.Tx, refID string) ([]refTarget, error) {
	rs, err := txQuery(ctx, tx,
		`SELECT DISTINCT entity_table, entity_id, field FROM "`+tblRefs+`" WHERE ref_id = ? ORDER BY 1, 2, 3`, refID)
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
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		for _, p := range c.st.plans {
			tq, err := ident(p.def.Name.String())
			if err != nil {
				return err
			}
			for _, f := range textFields(p) {
				fq, err := ident(f)
				if err != nil {
					return err
				}
				for _, needle := range needles {
					got, err := c.reexpandWhere(ctx, tx, p, f,
						`SELECT "`+colID+`", `+fq+` FROM `+tq+` WHERE instr(`+fq+`, ?) > 0`, needle)
					if err != nil {
						return err
					}
					n += got
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
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
		if err := c.expandField(ctx, tx, p.def.Name, h.id, f, h.text); err != nil {
			return 0, err
		}
	}
	return len(hits), nil
}
