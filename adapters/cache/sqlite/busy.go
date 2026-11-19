package sqlite

// busy.go — waiting for a lock another lore process (or connection) holds
// on a SQLite file: the first connection, a write transaction, a check.

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// connectPoll is how often a busy connect, begin or check is asked again.
const connectPoll = 10 * time.Millisecond

// lockWaitCeiling bounds how long a lock that stays busy is waited for
// when ctx has no deadline of its own. Why a ceiling and not a short fixed
// wait: a busy SQLite lock always has a live holder (the OS drops a dead
// process's locks), and that holder's transaction takes as long as its
// work, which grows with machine load. The old fixed waits (5 s to
// connect, 20 s to check, 60 s to begin) were sized for an idle machine:
// creating a fresh cache takes ~0.7 s under -race alone and over 60 s at
// load ~100, so every opener queued behind the creator failed "database
// is locked" or hit its deadline while the creator was still working
// (devco:D-166). The ceiling only guards against a holder that never
// finishes (a stopped process), far above any lock hold seen.
const lockWaitCeiling = 10 * time.Minute

// lockWait is one wait for a busy lock: retry until the lock is had, ctx
// ends, or lockWaitCeiling passes.
type lockWait struct{ deadline time.Time }

// newLockWait starts a wait now.
func newLockWait() lockWait { return lockWait{deadline: time.Now().Add(lockWaitCeiling)} }

// again reports whether err (an attempt's answer) is a busy lock worth
// asking for again, after sleeping connectPoll. It returns false at once
// for success, any other error, or a wait past the ceiling, and false
// after the sleep when ctx ended meanwhile. Each attempt already waits
// busy_timeout inside SQLite where SQLite honours it; this loop covers
// the busy answers it does not wait for (switching a new file to WAL,
// SQLITE_BUSY_RECOVERY) and a writer that loses every turn of SQLite's
// unfair busy handler while others commit.
func (w lockWait) again(ctx context.Context, err error) bool {
	if err == nil || !isBusy(err) || !time.Now().Before(w.deadline) {
		return false
	}
	t := time.NewTimer(connectPoll)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// connectBusy opens db's connection (the DSN's pragmas run then), asking
// again while it is busy (lockWait). Switching a new file to WAL takes a
// lock SQLite does not wait for (busy_timeout does not cover it), so
// several lore processes opening a fresh machine.db or cache at once
// failed the losers at once with "database is locked". A ctx that ended
// is reported with the last busy answer.
func connectBusy(ctx context.Context, db *sql.DB) error {
	w := newLockWait()
	for {
		err := db.PingContext(ctx)
		if !w.again(ctx, err) {
			return withCtxErr(ctx, err)
		}
	}
}

// beginWrite starts a write transaction (BEGIN IMMEDIATE, the DSN's
// _txlock), asking again while the lock is busy (lockWait): an audit
// chain append or a cache open queued behind a long writer waits for it
// instead of failing "database is locked".
func beginWrite(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	w := newLockWait()
	for {
		tx, err := db.BeginTx(ctx, nil)
		if !w.again(ctx, err) {
			return tx, withCtxErr(ctx, err)
		}
	}
}

// withCtxErr adds ctx's error to a busy err when ctx ended, so the caller
// sees both why it stopped waiting and what it was waiting for.
func withCtxErr(ctx context.Context, err error) error {
	if err != nil && isBusy(err) && ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	return err
}
