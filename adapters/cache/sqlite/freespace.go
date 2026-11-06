package sqlite

// freespace.go — the free-space probe of a failed open (E-FATES-3, port of
// v0.1.17 guard.FreeBytes / guard.DiskFull and explainStorageError CH-7).
//
// A full disk usually does not reach SQLite as SQLITE_FULL: before it can
// even read, SQLite creates the -wal and -shm files, and when that fails
// the open answers SQLITE_CANTOPEN ("unable to open database file"), which
// alone says nothing about space. So an open that fails with CANTOPEN asks
// the filesystem how much room is left; under lowSpaceBytes the error is
// E_DISK_FULL with its hint. The open is the first thing a command does
// with the file, so this answers before any write. ExplainDiskFull (the
// host's hook) stays the second layer for SQLITE_FULL / ENOSPC later on.

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"

	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/thesatellite-ai/lore/model/errs"
)

// lowSpaceBytes is the free space below which a failing open is reported
// as a full disk: SQLite needs room for its WAL and shared-memory files
// before it can read (v0.1.17 guard.LowSpaceBytes, 1 MiB).
const lowSpaceBytes = 1 << 20

// cantOpenMarker is SQLite's text for SQLITE_CANTOPEN (14), for an error
// that lost its *msqlite.Error on the way up.
const cantOpenMarker = "unable to open database file"

// freeBytesFunc answers the bytes available to this user on the filesystem
// holding a path, -1 when unknown. FreeBytes in production; the seam lets
// a test say "the disk is full" without filling one.
type freeBytesFunc func(path string) int64

// DiskFull reports whether the filesystem holding p is (nearly) out of
// space. Unknown free space (-1) is never reported as full.
func DiskFull(p string) bool { return lowOnSpace(FreeBytes(p)) }

// lowOnSpace reports a known free-space figure under lowSpaceBytes.
func lowOnSpace(free int64) bool { return free >= 0 && free < lowSpaceBytes }

// freeOf is blocks × block size, saturated at math.MaxInt64 (a figure
// that large is "plenty" either way; it must not wrap negative, which
// would read as unknown).
func freeOf(blocks, size uint64) int64 {
	if size == 0 {
		return -1
	}
	if blocks > math.MaxInt64/size {
		return math.MaxInt64
	}
	n := blocks * size // cannot wrap: blocks <= MaxInt64/size; the check below states the int64 bound at the conversion
	if n > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}

// existingAncestor is p or its nearest existing parent (the probe of a
// path not created yet asks the folder it will be created in).
func existingAncestor(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// isCantOpen reports SQLITE_CANTOPEN, by code or by its message text.
func isCantOpen(err error) bool {
	var se *msqlite.Error
	if errors.As(err, &se) && se.Code()&primaryCodeMask == sqlite3.SQLITE_CANTOPEN {
		return true
	}
	return strings.Contains(err.Error(), cantOpenMarker)
}

// explainOpen explains the failed open of the SQLite file at path: a
// disk-full error is E_DISK_FULL (ExplainDiskFull); a CANTOPEN is
// E_DISK_FULL only when free reports less than lowSpaceBytes in the
// file's folder (or its nearest existing parent). An error carrying a lore
// code other than E_INTERNAL keeps it, and anything else is returned
// unchanged.
//
// Invariant: never turns a non-nil error into nil.
func explainOpen(path string, err error, free freeBytesFunc) error {
	if err == nil {
		return nil
	}
	if IsDiskFull(err) {
		return ExplainDiskFull(err)
	}
	if !isCantOpen(err) {
		return err
	}
	var le *errs.Error
	if errors.As(err, &le) && le.Code != errs.Internal {
		return err
	}
	if !lowOnSpace(free(existingAncestor(filepath.Dir(path)))) {
		return err
	}
	return errs.Wrap(errs.DiskFull, diskFullMessage, err).WithHint(diskFullHint)
}
