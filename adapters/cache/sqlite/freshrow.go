package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"strings"
)

// A cache build from nothing (the first read after the cache file is
// gone, a new clone) applies every row of a big store once. Each row's
// projection used to clear what an older copy of the row left first: its
// FTS entry, its row_refs and row_expanded lines per text field, its join
// pairs, its copy in every kind table. For a row whose id the file has
// never held all of those deletes find nothing, and on a 1.26M-row store
// they were a fifth of the build (bug hunt H-282, devco:D-1020). A fresh
// row is projected with plain inserts instead.
//
// Fresh means: before this Apply stored it, no copy of the id was in
// `_rows` (any source, any table), and nothing in this transaction has
// projected it yet. Every typed, FTS, join and reference row is written
// for a row of `_rows` and goes with it (Rebuild), so an id absent from
// `_rows` has none. A side path that projects rows of the same Apply
// before their turn (a meta key re-index, a kind table refill) goes
// through the full replace path, which marks the row touched.

// qIDHeld finds any copy of an id in `_rows` (ix__rows_id).
const qIDHeld = `SELECT 1 FROM "` + tblRows + `" WHERE id = ? LIMIT 1`

// idHeld reports whether `_rows` holds any copy of id.
func idHeld(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var one int
	err := txQueryRow(ctx, tx, qIDHeld, id).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, dbErr("find held row", err)
	}
	return true, nil
}

// touchedKey names one row of one table in txStmts.touched.
func touchedKey(table, id string) string { return table + "\x00" + id }

// markTouched records that tx projected (table, id) of source through the
// full replace path, and drops the FTS row queued for it: that path wrote
// the row's current FTS entry itself (or deleted the row). A transaction
// inTx did not open keeps no set: no fresh row is projected in it.
func markTouched(tx *sql.Tx, table, source, id string) {
	v, ok := openTxs.Load(tx)
	if !ok {
		return
	}
	s := v.(*txStmts)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.touched == nil {
		s.touched = map[string]struct{}{}
	}
	s.touched[touchedKey(table, id)] = struct{}{}
	if q, ok := s.ftsAt[ftsKey(table, source, id)]; ok {
		q.dropped = true
	}
}

// wasTouched reports whether tx projected (table, id) already.
func wasTouched(tx *sql.Tx, table, id string) bool {
	v, ok := openTxs.Load(tx)
	if !ok {
		return true // unknown transaction: never take the fresh path
	}
	s := v.(*txStmts)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok = s.touched[touchedKey(table, id)]
	return ok
}

// FTS5 writes the terms it holds in memory to disk at every statement
// savepoint, and most statements of an Apply open one. One FTS insert per
// row between the other statements therefore wrote one tiny index segment
// per row, and FTS5 spent the build merging them. A fresh row's FTS entry
// is queued instead and the queue goes in ftsBatch rows per statement
// (ftsInsertMany): one segment per batch.

// ftsBatch is how many queued FTS rows one statement writes; it also
// bounds what a queue holds.
const ftsBatch = 1000

// ftsQueue is the queued FTS rows of one FTS table.
type ftsQueue struct {
	many  string // the plan's ftsInsertMany
	items []*ftsQueued
}

// ftsQueued is one queued FTS row: ftsInsert's JSON array. dropped: the
// full replace path wrote the row after it was queued (markTouched).
type ftsQueued struct {
	key     string // ftsKey
	cells   []byte
	dropped bool
}

// ftsKey names one queued FTS row.
func ftsKey(table, source, id string) string { return table + "\x00" + source + "\x00" + id }

// queueFTS queues the FTS row cells of (p, source, id) in tx, writing the
// table's queue when it is full. A transaction inTx did not open writes
// the row at once.
func queueFTS(ctx context.Context, tx *sql.Tx, p plan, source, id string, cells []byte) error {
	v, ok := openTxs.Load(tx)
	if !ok {
		if _, err := txExec(ctx, tx, p.sql.ftsInsert, string(cells)); err != nil {
			return dbErr("fts insert", err)
		}
		return nil
	}
	s := v.(*txStmts)
	s.mu.Lock()
	if s.fts == nil {
		s.fts, s.ftsAt = map[string]*ftsQueue{}, map[string]*ftsQueued{}
	}
	q, ok := s.fts[p.fts]
	if !ok {
		q = &ftsQueue{many: p.sql.ftsInsertMany}
		s.fts[p.fts] = q
	}
	item := &ftsQueued{key: ftsKey(p.def.Name.String(), source, id), cells: cells}
	q.items = append(q.items, item)
	s.ftsAt[item.key] = item
	full := len(q.items) >= ftsBatch
	s.mu.Unlock()
	if full {
		return flushFTS(ctx, tx, p.fts)
	}
	return nil
}

// flushFTS writes the queued FTS rows of tx: of the FTS table only when
// given, else of every table. An Apply flushes before it records its
// cursor, so nothing queued outlives the transaction.
func flushFTS(ctx context.Context, tx *sql.Tx, only string) error {
	v, ok := openTxs.Load(tx)
	if !ok {
		return nil
	}
	s := v.(*txStmts)
	type batch struct{ many, arr string }
	var todo []batch
	s.mu.Lock()
	names := slices.Sorted(maps.Keys(s.fts))
	for _, name := range names {
		if only != "" && name != only {
			continue
		}
		q := s.fts[name]
		var b strings.Builder
		b.WriteByte('[')
		n := 0
		for _, it := range q.items {
			if s.ftsAt[it.key] == it {
				delete(s.ftsAt, it.key) // written below: it can no longer be dropped
			}
			if it.dropped {
				continue
			}
			if n > 0 {
				b.WriteByte(',')
			}
			b.Write(it.cells)
			n++
		}
		b.WriteByte(']')
		if n > 0 {
			todo = append(todo, batch{q.many, b.String()})
		}
		delete(s.fts, name)
	}
	s.mu.Unlock()
	for _, b := range todo {
		if _, err := txExec(ctx, tx, b.many, b.arr); err != nil {
			return dbErr("fts insert", err)
		}
	}
	return nil
}
