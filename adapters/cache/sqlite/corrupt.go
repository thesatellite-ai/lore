package sqlite

import (
	"errors"

	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsCorrupt reports whether err means the cache FILE is damaged
// (SQLITE_CORRUPT "database disk image is malformed", SQLITE_NOTADB "file
// is not a database"), as opposed to locked, missing or not permitted.
//
// Why it exists: a cache is rebuildable from the row files (spec 3), so a
// caller may move a damaged file aside and rebuild; a lock or permission
// error must never take that path, because the file is fine and another
// process may be using it.
func IsCorrupt(err error) bool {
	var se *msqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() & primaryCodeMask {
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
		return true
	}
	return false
}
