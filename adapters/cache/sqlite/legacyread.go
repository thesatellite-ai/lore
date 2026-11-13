package sqlite

import (
	"errors"
	"io/fs"
	"os"
)

// LegacyReadQuery is the sqlite URI query of a read-only open of a lore
// 0.1.17 database file (a project's .lore/lore.db or a shared one) at
// path. Why not plain mode=ro: a read-only open of a WAL database still
// creates its -wal and -shm files beside it, and in a format-1 clone that
// is the work tree, so a CI read changed it (F-8). With no -wal file beside
// path there is nothing to replay, and the file is read as it is on disk
// (immutable=1: no sidecar is made); with one, the plain read-only open
// keeps reading what the WAL holds. frozen forces immutable (a dry run
// writes nothing, B4-14). extra is appended as more query parameters
// ("" for none).
func LegacyReadQuery(path string, frozen bool, extra string) string {
	q := "mode=ro"
	if frozen || !hasWAL(path) {
		q += "&immutable=1"
	}
	if extra != "" {
		q += "&" + extra
	}
	return q
}

// hasWAL reports whether a -wal file may exist beside the database at path
// (a stat error other than not-exist counts as one, so the safe open is
// kept).
func hasWAL(path string) bool {
	_, err := os.Lstat(path + "-wal")
	return !errors.Is(err, fs.ErrNotExist)
}
