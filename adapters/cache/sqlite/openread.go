package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
)

// Opening a cache used to take the write lock (BEGIN IMMEDIATE) on every
// open, a read command's included, to create the internal tables "IF NOT
// EXISTS" and compare each table's fingerprint. With many lore processes
// at once (ten agents in one repo, devco:D-159) every command queued
// behind every other one's open, and p95 went past 5 s. initRead checks
// first, in a read transaction, whether the file is already current; only
// a file that needs a change takes the write lock (init's write path).

// ddlStamp is a hash of the internal DDL this binary runs on open, kept in
// the file's PRAGMA application_id once that DDL has run. Why a hash: the
// internal DDL gains statements without a layoutVersion bump (CREATE ... IF
// NOT EXISTS is safe to run again), so the version alone cannot tell a
// reader that every internal object exists.
var ddlStamp = func() int32 {
	h := fnv.New32a()
	for _, s := range internalDDL {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	for _, s := range taskTreeDDL {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte(strconv.Itoa(layoutVersion)))
	return int32(h.Sum32()) // application_id is a signed 32-bit header field
}()

// qStampDDL records ddlStamp in the file header (init's write path, after
// the DDL ran).
var qStampDDL = "PRAGMA application_id = " + strconv.Itoa(int(ddlStamp))

// initRead loads the state of a cache file that needs no change, inside
// one read transaction (no write lock). ok is false when anything is out
// of date: another layout or internal DDL, a task tree to fill, a table
// missing, extra or with another fingerprint. Then init takes the write
// path, which re-checks everything under the lock.
func (c *Cache) initRead(ctx context.Context) (state, bool, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}) // a plain BEGIN: deferred, no lock until the first read
	if err != nil {
		return state{}, false, dbErr("begin read", err)
	}
	defer func() { _ = tx.Rollback() }() // read only: nothing to keep
	var ver, app int64
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return state{}, false, dbErr("read layout version", err)
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&app); err != nil {
		return state{}, false, dbErr("read layout stamp", err)
	}
	if ver != layoutVersion || int32(app) != ddlStamp {
		return state{}, false, nil
	}
	var fill bool
	if err := tx.QueryRowContext(ctx, qTreeNeedsFill).Scan(&fill); err != nil {
		if isMissingTable(err) {
			return state{}, false, nil
		}
		return state{}, false, dbErr("task_tree: check", err)
	}
	if fill {
		return state{}, false, nil
	}
	st, err := c.loadState(ctx, tx)
	if err != nil {
		if isMissingTable(err) {
			return state{}, false, nil
		}
		return state{}, false, err
	}
	have, err := readRecorded(ctx, tx)
	if err != nil {
		if isMissingTable(err) {
			return state{}, false, nil
		}
		return state{}, false, err
	}
	if len(have) != len(st.plans) {
		return state{}, false, nil
	}
	for name, p := range st.plans {
		if rec, ok := have[name.String()]; !ok || rec.fingerprint != p.fingerprint {
			return state{}, false, nil
		}
	}
	return st, true, nil
}

// isMissingTable reports SQLite's "no such table" anywhere in err's chain:
// a stamped file someone emptied by hand is not current, not broken.
func isMissingTable(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), "no such table") {
			return true
		}
	}
	return false
}
