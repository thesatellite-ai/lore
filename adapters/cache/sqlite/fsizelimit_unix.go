//go:build linux || darwin

package sqlite

import "golang.org/x/sys/unix"

// fileSizeLimited reports whether this process runs under a finite
// RLIMIT_FSIZE (`ulimit -f`): a file write past it fails with EFBIG, which
// SQLite reports only as SQLITE_IOERR_WRITE with no errno attached.
func fileSizeLimited() bool {
	var r unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &r); err != nil {
		return false
	}
	return r.Cur != unix.RLIM_INFINITY
}
