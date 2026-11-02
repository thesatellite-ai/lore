//go:build windows

package processlock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// errLockBusy is the internal sentinel returned when LockFileEx reports the
// range is held by another handle.
var errLockBusy = errors.New("processlock: lock busy")

// The locked range is one byte far beyond any real content. Windows byte-range
// locks are mandatory: a locked region cannot be read through other handles.
// Locking past EOF (which Windows allows) keeps the pid line at offset 0
// readable by ownerAlive and diagnostics while the lock is held.
const (
	lockOffsetLow  = 0
	lockOffsetHigh = 0x7FFFFFFF
	lockLenLow     = 1
	lockLenHigh    = 0
)

// lockOverlapped returns the OVERLAPPED that addresses the lock range.
func lockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: lockOffsetLow, OffsetHigh: lockOffsetHigh}
}

// tryAcquire opens path O_CREATE|O_RDWR and takes an exclusive, non-blocking
// LockFileEx lock on the lock range. An existing file — including one left
// behind by an older lore that used O_EXCL file existence as the lock — is
// reused, so it can never block forever.
func tryAcquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(h, flags, 0, lockLenLow, lockLenHigh, lockOverlapped()); err != nil {
		_ = f.Close() // the lock error is the one worth reporting
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errLockBusy
		}
		return nil, fmt.Errorf("processlock: LockFileEx: %w", err)
	}
	unlock := func() error {
		return windows.UnlockFileEx(h, 0, lockLenLow, lockLenHigh, lockOverlapped())
	}
	if err := writeContent(f); err != nil {
		_ = unlock()  // closing below releases the range regardless
		_ = f.Close() // the write error is the one worth reporting
		return nil, err
	}
	return &Lock{path: path, file: f, owned: true, unlock: unlock}, nil
}
