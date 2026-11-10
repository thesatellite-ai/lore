package sqlite

// openerr.go — the error of a cache or machine-store file SQLite could not
// open: it names the file, SQLite's own error and what to do next.

import (
	"errors"
	"fmt"

	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/thesatellite-ai/lore/model/errs"
)

// openBusyHint is the next step when the file stayed locked past
// busyTimeoutMS: another lore process (lore api, a TUI, a sync or a hook)
// holds it, so waiting and running the command again is the fix.
const openBusyHint = "another lore process is holding %s (lore api, a TUI, a sync or an agent hook); wait for it to finish and run the command again"

// openFailHint is the next step for any other open failure: the cache is
// derived from the row files, so moving the file aside loses nothing.
const openFailHint = "check that %s is readable and writable and its disk has space (df -h); if it still fails, move it aside (mv %s %s.bad) and run the command again: lore rebuilds it from the row files"

// openErr is the error of a failed first connection to the file at path
// (what is "cache" or "machine store"). The message names SQLite's error
// and code, never only "sqlite cache: open": that text hid whether the
// file was locked, unreadable or damaged (machines sweep, 2026-11-10).
// The cause stays in the chain, so explainOpen still sees SQLITE_CANTOPEN
// and a cancelled context is still errors.Is(err, context.Canceled).
func openErr(what, path string, err error) error {
	hint := fmt.Sprintf(openFailHint, path, path, path)
	if isBusy(err) {
		hint = fmt.Sprintf(openBusyHint, path)
	}
	return errs.Wrap(errs.Internal, fmt.Sprintf("sqlite cache: open the %s %s: %s", what, path, driverText(err)), err).WithHint(hint)
}

// isBusy reports SQLITE_BUSY (any extended code) in err's chain.
func isBusy(err error) bool {
	var se *msqlite.Error
	return errors.As(err, &se) && se.Code()&primaryCodeMask == sqlite3.SQLITE_BUSY
}

// driverText is SQLite's message for err with its code name, e.g.
// "database is locked (5) (SQLITE_BUSY)"; a non-SQLite error (a cancelled
// context) is its own text.
func driverText(err error) string {
	var se *msqlite.Error
	if errors.As(err, &se) {
		return se.Error()
	}
	return err.Error()
}
