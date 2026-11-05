package sqlite

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
)

// QuickCheckOK is PRAGMA quick_check's one-row answer for a sound file.
const QuickCheckOK = "ok"

// QuickCheck runs PRAGMA quick_check on the SQLite file at path, opened
// read-only (nothing is created or written), and returns its first answer
// row: QuickCheckOK for a sound file, a description of the damage
// otherwise. A file damaged past its first pages still opens, so only
// this check sees it (`lore doctor`, wire.DoctorCaches). The error is the
// driver's own: IsCorrupt tells damage from a lock or permission problem.
func QuickCheck(ctx context.Context, path string) (string, error) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	db, err := sql.Open(driverName, u.String())
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to lose on close
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res); err != nil {
		return "", err
	}
	return res, nil
}
