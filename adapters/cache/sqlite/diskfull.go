package sqlite

import (
	"errors"
	"strings"
	"syscall"

	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fullMarker is SQLite's text for SQLITE_FULL (13), as it appears in an
// error that lost its *msqlite.Error on the way up (wrapped with %v, or
// re-made by a caller).
const fullMarker = "database or disk is full"

// Messages of E_DISK_FULL: v0.1.17's text (saas/cmd/cli explainStorageError,
// CH-7), so scripts and docs that quote it keep working.
const (
	diskFullMessage = "the disk is full: lore could not write (nothing was changed)"
	diskFullHint    = "free some space and run the command again; the row files and the cache are intact"
)

// IsDiskFull reports whether err means the disk holding a lore file is
// full: SQLite's SQLITE_FULL (by code, or by its message text when the
// driver error was flattened) or the OS's ENOSPC from a file write.
//
// Why: without it a full disk surfaced as E_INTERNAL (exit 1, "internal
// error"), which tells a person to file a bug instead of freeing space.
func IsDiskFull(err error) bool {
	if err == nil {
		return false
	}
	var se *msqlite.Error
	if errors.As(err, &se) && se.Code()&primaryCodeMask == sqlite3.SQLITE_FULL {
		return true
	}
	return errors.Is(err, syscall.ENOSPC) || strings.Contains(err.Error(), fullMarker)
}

// ExplainDiskFull turns a disk-full failure into E_DISK_FULL (class refused,
// exit 23) with a hint, keeping err as the cause. Anything else, and an
// error that already carries a lore code other than E_INTERNAL (the code a
// caller chose wins), is returned unchanged.
//
// Invariant: never turns a non-nil error into nil, never touches an error
// that is not a disk-full one.
func ExplainDiskFull(err error) error {
	if !IsDiskFull(err) {
		return err
	}
	var le *errs.Error
	if errors.As(err, &le) && le.Code != errs.Internal {
		return err
	}
	return errs.Wrap(errs.DiskFull, diskFullMessage, err).WithHint(diskFullHint)
}
