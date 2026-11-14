package sqlite

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// QuickCheckOK is PRAGMA quick_check's one-row answer for a sound file.
const QuickCheckOK = "ok"

// QuickCheck runs PRAGMA quick_check on the SQLite file at path, opened
// read-only (nothing is created or written), and returns its first answer
// row: QuickCheckOK for a sound file, a description of the damage
// otherwise. A file damaged past its first pages still opens, so only
// this check sees it (`lore doctor`, wire.DoctorCaches). A file another
// process holds is waited for (busy_timeout, then asked again until ctx
// ends, busy.go's lockWait: owner item 35, eight `lore fix --all` children
// each ran doctor over every cache while their siblings wrote theirs, and
// a busy file came back "cannot check: database is locked (261)"); the
// last error is the driver's own: IsCorrupt tells damage from a
// permission problem, IsBusy a file that stayed in use past the wait.
func QuickCheck(ctx context.Context, path string) (string, error) {
	db, err := sql.Open(driverName, fileuri.URL(path, "mode=ro&_pragma=busy_timeout("+strconv.Itoa(busyTimeoutMS)+")"))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to lose on close
	w := newLockWait()
	for {
		var res string
		err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res)
		if err == nil {
			return res, nil
		}
		if !w.again(ctx, err) {
			return "", err
		}
	}
}

// IsBusy reports whether err is SQLite's "database is locked" (SQLITE_BUSY,
// any extended code such as 261, SQLITE_BUSY_RECOVERY): another process
// holds the file, which says nothing about the file being damaged.
func IsBusy(err error) bool { return isBusy(err) }
