package sqlite

// busy.go — the first connection of a file opened by several lore
// processes at once.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// connectPoll is how often connectBusy retries a first connection that
// met SQLITE_BUSY.
const connectPoll = 10 * time.Millisecond

// connectBusy opens db's connection (the DSN's pragmas run then), retrying
// while it fails with SQLITE_BUSY, for up to busyTimeoutMS. Switching a
// new file to WAL takes a lock SQLite does not wait for (busy_timeout does
// not cover it), so several lore processes opening a fresh machine.db or
// cache at once failed the losers at once with "database is locked".
func connectBusy(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(busyTimeoutMS * time.Millisecond)
	for {
		err := db.PingContext(ctx)
		var se *msqlite.Error
		if err == nil || !errors.As(err, &se) || se.Code()&primaryCodeMask != sqlite3.SQLITE_BUSY || !time.Now().Before(deadline) {
			return err
		}
		t := time.NewTimer(connectPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
