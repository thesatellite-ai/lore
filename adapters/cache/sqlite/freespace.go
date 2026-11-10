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
	"fmt"
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

// roomFor is the free blocks a new file can use: blocks, or 0 when the
// filesystem counts inodes (files > 0) and has none left. A filesystem
// that reports no inode count (files == 0) is judged by blocks alone.
func roomFor(blocks, files, ffree uint64) uint64 {
	if files > 0 && ffree == 0 {
		return 0
	}
	return blocks
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
	dir := existingAncestor(filepath.Dir(path))
	if !lowOnSpace(free(dir)) {
		return err
	}
	return errs.Wrap(errs.DiskFull, fmt.Sprintf(noRoomMessage, path, dir), err).WithHint(fmt.Sprintf(noRoomHint, dir, dir))
}

// noRoomMessage and noRoomHint are a failed open on a filesystem with no
// room for a new file: they name the file, the folder and both limits a
// person checks (inodes and bytes), because a tmpfs at its inode cap has
// gigabytes free and "the disk is full" alone sent people to df -h, which
// showed nothing wrong (machines sweep, 2026-11-10).
const (
	noRoomMessage = "cannot create the cache file %s: no free inodes or disk space in %s (nothing was changed)"
	noRoomHint    = "free some files or space there (df -i %s, df -h %s) and run the command again; the row files are intact and the cache is rebuilt from them"
)

// writeFailHint is the hint of a cache write that failed with an I/O error
// on a disk that still reports free space: lore cannot tell a quota (or a
// failing disk) from here, so it names the file and both things to check.
const writeFailHint = "a write to %s failed: check free space and your quota on that disk (df -h, quota -s), then run the command again; the row files are intact and the cache is rebuilt from them"

// isWriteIOErr reports SQLite's SQLITE_IOERR family (a write, fsync or
// truncate the OS refused), by code.
func isWriteIOErr(err error) bool {
	var se *msqlite.Error
	return errors.As(err, &se) && se.Code()&primaryCodeMask == sqlite3.SQLITE_IOERR
}

// fileWrites is the write side of one SQLite file lore keeps (the cache,
// the machine store, the audit chain, local state, the doctor cache): its
// path and the free-space probe. Each of them sends every write error
// through explain, so a write under a full quota is E_DISK_FULL and any
// other write I/O error names its file, whichever file it was.
type fileWrites struct {
	path string
	free freeBytesFunc // FreeBytes in production; a test says "no room left"
}

// writesTo is the fileWrites of the file at path, with the real probe.
func writesTo(path string) fileWrites { return fileWrites{path: path, free: FreeBytes} }

// explain is explainWrite for this file.
func (w fileWrites) explain(err error) error { return explainWrite(w.path, err, w.free) }

// explainWrite explains a failed write to the cache file at path. SQLite
// reports a full disk under a quota (EDQUOT) or a file-size limit (EFBIG)
// as a plain SQLITE_IOERR with no errno, so after one the filesystem is
// asked: no room left for this user (statfs, under lowSpaceBytes) is
// E_DISK_FULL with its hint; room left keeps the error, now naming the file
// and what to check, never a bare E_INTERNAL. Disk-full errors of any other
// form and every other error are returned unchanged.
//
// Invariant: never turns a non-nil error into nil.
func explainWrite(path string, err error, free freeBytesFunc) error {
	if err == nil || IsDiskFull(err) || !isWriteIOErr(err) {
		return err
	}
	if free != nil && lowOnSpace(free(existingAncestor(filepath.Dir(path)))) {
		return errs.Wrap(errs.DiskFull, diskFullMessage, err).WithHint(diskFullHint)
	}
	return errs.Wrap(errs.Internal, "sqlite cache: write "+path, err).WithHint(fmt.Sprintf(writeFailHint, path))
}
