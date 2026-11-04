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
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return 0, err
	}
	n := 0
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		targets, err := referrers(ctx, tx, refID)
		if err != nil {
			return err
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
			err := tx.QueryRowContext(ctx, sel, t.id).Scan(&text)
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
	rs, err := tx.QueryContext(ctx,
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
