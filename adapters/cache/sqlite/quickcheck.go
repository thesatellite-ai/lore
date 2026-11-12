package sqlite

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// QuickCheckOK is PRAGMA quick_check's one-row answer for a sound file.
const QuickCheckOK = "ok"

// quickCheckWait bounds how long QuickCheck keeps asking while another
// lore process holds the file (owner item 35: eight `lore fix --all`
// children each ran doctor over every cache under ~/.lore/cache while
// their siblings wrote theirs, and a busy file came back as "cannot
// check: database is locked (261)", doctor BROKEN). Each attempt already
// waits busy_timeout inside SQLite; the loop covers the busy answers
// SQLite does not wait for (SQLITE_BUSY_RECOVERY while another connection
// replays the WAL, the first read of a WAL file being switched over).
const quickCheckWait = 20 * time.Second

// QuickCheck runs PRAGMA quick_check on the SQLite file at path, opened
// read-only (nothing is created or written), and returns its first answer
// row: QuickCheckOK for a sound file, a description of the damage
// otherwise. A file damaged past its first pages still opens, so only
// this check sees it (`lore doctor`, wire.DoctorCaches). A file another
// process holds is waited for (busy_timeout, then retried up to
// quickCheckWait or until ctx ends); the last error is the driver's own:
// IsCorrupt tells damage from a permission problem, IsBusy a file that
// stayed in use past the wait.
func QuickCheck(ctx context.Context, path string) (string, error) {
	db, err := sql.Open(driverName, fileuri.URL(path, "mode=ro&_pragma=busy_timeout("+strconv.Itoa(busyTimeoutMS)+")"))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to lose on close
	deadline := time.Now().Add(quickCheckWait)
	for {
		var res string
		err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res)
		if err == nil {
			return res, nil
		}
		if !isBusy(err) || !time.Now().Before(deadline) {
			return "", err
		}
		t := time.NewTimer(connectPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return "", err
		case <-t.C:
		}
	}
}

// IsBusy reports whether err is SQLite's "database is locked" (SQLITE_BUSY,
// any extended code such as 261, SQLITE_BUSY_RECOVERY): another process
// holds the file, which says nothing about the file being damaged.
func IsBusy(err error) bool { return isBusy(err) }
