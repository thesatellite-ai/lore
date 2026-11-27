package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/model/errs"
)

// A cache refresh runs the same few statements once per row (upsert the
// typed row, its FTS entry, its refs, the raw copy), and database/sql
// prepares a statement again on every tx.ExecContext: preparing was half
// of a 5,000-row refresh (item 41). inTx keeps each statement prepared
// once per transaction; txExec and txQueryRow use it (and are
// plain tx calls for a transaction inTx did not open). The statements
// close with the transaction.
type txStmts struct {
	mu sync.Mutex
	tx *sql.Tx
	by map[string]*sql.Stmt
	// touched holds table+"\x00"+id of every row the transaction wrote
	// through the full replace path (upsertTyped and friends with fresh
	// false): such a row is no longer fresh (freshrow.go).
	touched map[string]struct{}
	// fts queues the FTS rows of fresh rows by FTS table, ftsAt each
	// queued row by table, source and id (freshrow.go).
	fts   map[string]*ftsQueue
	ftsAt map[string]*ftsQueued
}

// deletesRun counts the DELETE statements txExec ran and queriesRun the
// queries txQuery ran, so a test can tell that a first build clears
// nothing and a re-expansion asks in batches (freshrow.go, H-282).
var deletesRun, queriesRun atomic.Int64

// prepares counts the statements stmt prepared, so a test can tell that a
// refresh prepares each statement once, not once per row (migfix:n2).
var prepares atomic.Int64

// openTxs maps each transaction inTx has open to its statements.
var openTxs sync.Map // *sql.Tx → *txStmts

func trackTx(tx *sql.Tx) func() {
	openTxs.Store(tx, &txStmts{tx: tx, by: map[string]*sql.Stmt{}})
	return func() { openTxs.Delete(tx) }
}

// stmt returns tx's prepared q, or nil when tx is not tracked or q cannot
// be prepared (the caller then runs q unprepared, which reports the error).
func stmt(ctx context.Context, tx *sql.Tx, q string) *sql.Stmt {
	v, ok := openTxs.Load(tx)
	if !ok {
		return nil
	}
	s := v.(*txStmts)
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.by[q]; ok {
		return st
	}
	st, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return nil
	}
	prepares.Add(1)
	s.by[q] = st
	return st
}

// maxArgs is the most arguments a statement of this cache takes (keepRaw:
// six). The arguments are driver values, spread by count, so no untyped
// value list is built (internal/layers TestNoAny).
const maxArgs = 6

// errArgs reports a statement given more than maxArgs arguments.
func errArgs(n int) error {
	return errs.New(errs.Internal, "sqlite cache: a statement got "+strconv.Itoa(n)+" arguments, at most "+strconv.Itoa(maxArgs))
}

// txExec runs q in tx with args: prepared once per transaction inTx opened
// (stmt), else a plain tx call.
func txExec(ctx context.Context, tx *sql.Tx, q string, a ...driver.Value) (sql.Result, error) {
	if strings.HasPrefix(q, "DELETE") {
		deletesRun.Add(1)
	}
	st := stmt(ctx, tx, q)
	switch len(a) {
	case 0:
		if st != nil {
			return st.ExecContext(ctx)
		}
		return tx.ExecContext(ctx, q)
	case 1:
		if st != nil {
			return st.ExecContext(ctx, a[0])
		}
		return tx.ExecContext(ctx, q, a[0])
	case 2:
		if st != nil {
			return st.ExecContext(ctx, a[0], a[1])
		}
		return tx.ExecContext(ctx, q, a[0], a[1])
	case 3:
		if st != nil {
			return st.ExecContext(ctx, a[0], a[1], a[2])
		}
		return tx.ExecContext(ctx, q, a[0], a[1], a[2])
	case 4:
		if st != nil {
			return st.ExecContext(ctx, a[0], a[1], a[2], a[3])
		}
		return tx.ExecContext(ctx, q, a[0], a[1], a[2], a[3])
	case 5:
		if st != nil {
			return st.ExecContext(ctx, a[0], a[1], a[2], a[3], a[4])
		}
		return tx.ExecContext(ctx, q, a[0], a[1], a[2], a[3], a[4])
	case 6:
		if st != nil {
			return st.ExecContext(ctx, a[0], a[1], a[2], a[3], a[4], a[5])
		}
		return tx.ExecContext(ctx, q, a[0], a[1], a[2], a[3], a[4], a[5])
	}
	return nil, errArgs(len(a))
}

// txQuery stays unprepared: the rows of a shared statement would stay
// open while a loop over them ran the same query again.
func txQuery(ctx context.Context, tx *sql.Tx, q string, a ...driver.Value) (*sql.Rows, error) {
	queriesRun.Add(1)
	switch len(a) {
	case 0:
		return tx.QueryContext(ctx, q)
	case 1:
		return tx.QueryContext(ctx, q, a[0])
	case 2:
		return tx.QueryContext(ctx, q, a[0], a[1])
	case 3:
		return tx.QueryContext(ctx, q, a[0], a[1], a[2])
	case 4:
		return tx.QueryContext(ctx, q, a[0], a[1], a[2], a[3])
	case 5:
		return tx.QueryContext(ctx, q, a[0], a[1], a[2], a[3], a[4])
	case 6:
		return tx.QueryContext(ctx, q, a[0], a[1], a[2], a[3], a[4], a[5])
	}
	return nil, errArgs(len(a))
}

// txQueryRow is txExec's single-row query. More than maxArgs arguments
// run q with none, so the row reports SQLite's own argument-count error.
func txQueryRow(ctx context.Context, tx *sql.Tx, q string, a ...driver.Value) *sql.Row {
	st := stmt(ctx, tx, q)
	switch len(a) {
	case 0:
		if st != nil {
			return st.QueryRowContext(ctx)
		}
		return tx.QueryRowContext(ctx, q)
	case 1:
		if st != nil {
			return st.QueryRowContext(ctx, a[0])
		}
		return tx.QueryRowContext(ctx, q, a[0])
	case 2:
		if st != nil {
			return st.QueryRowContext(ctx, a[0], a[1])
		}
		return tx.QueryRowContext(ctx, q, a[0], a[1])
	case 3:
		if st != nil {
			return st.QueryRowContext(ctx, a[0], a[1], a[2])
		}
		return tx.QueryRowContext(ctx, q, a[0], a[1], a[2])
	case 4:
		if st != nil {
			return st.QueryRowContext(ctx, a[0], a[1], a[2], a[3])
		}
		return tx.QueryRowContext(ctx, q, a[0], a[1], a[2], a[3])
	case 5:
		if st != nil {
			return st.QueryRowContext(ctx, a[0], a[1], a[2], a[3], a[4])
		}
		return tx.QueryRowContext(ctx, q, a[0], a[1], a[2], a[3], a[4])
	case 6:
		if st != nil {
			return st.QueryRowContext(ctx, a[0], a[1], a[2], a[3], a[4], a[5])
		}
		return tx.QueryRowContext(ctx, q, a[0], a[1], a[2], a[3], a[4], a[5])
	}
	return tx.QueryRowContext(ctx, q)
}

// txExecN is txExec for a statement whose argument count is known only at
// run time (a typed insert: two per column, bindList), prepared the same
// way. Why the []any (TestNoAny allowlist): ExecContext takes ...any and
// the arity is the table's column count; every element is a driver.Value
// built by cellArgs, copied over as is.
func txExecN(ctx context.Context, tx *sql.Tx, q string, a []driver.Value) (sql.Result, error) {
	args := make([]any, len(a))
	for i, v := range a {
		args[i] = v
	}
	if st := stmt(ctx, tx, q); st != nil {
		return st.ExecContext(ctx, args...)
	}
	return tx.ExecContext(ctx, q, args...)
}
