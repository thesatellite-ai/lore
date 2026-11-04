//go:build windows

package rowfs

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// The locked range is one byte far past any content (processlock
// flock_windows.go): Windows byte-range locks are mandatory, and locking
// past EOF keeps the holder line readable for diagnostics.
const (
	lockOffsetHigh = 0x7FFFFFFF
	lockLenLow     = 1
	lockLenHigh    = 0
)

// lockOverlapped addresses the lock range.
func lockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: lockOffsetHigh}
}

// tryLock opens path and takes an exclusive, non-blocking LockFileEx lock.
func tryLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(h, flags, 0, lockLenLow, lockLenHigh, lockOverlapped()); err != nil {
		closeErr := f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errLockBusy
		}
		return nil, errors.Join(err, closeErr)
	}
	l := &fileLock{file: f, unlock: func() error {
		return windows.UnlockFileEx(h, 0, lockLenLow, lockLenHigh, lockOverlapped())
	}}
	if err := writeHolder(f); err != nil {
		return nil, errors.Join(err, l.release())
	}
	return l, nil
}
