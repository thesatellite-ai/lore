//go:build windows

package processlock

import (
	"errors"
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/windows"
)

// reclaimStaleOnBusy is false on Windows: LockFileEx reports busy only while
// some open handle holds the range, and the kernel drops the range when the
// holding process exits, so a busy lock always has a live holder. Deleting
// the file would also fail there (it is open without FILE_SHARE_DELETE).
const reclaimStaleOnBusy = false

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

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not exited (STILL_ACTIVE in the Windows SDK).
const stillActive = 259

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

// pidAlive returns true if pid is a running process. A process that exists
// but cannot be opened (another user's, access denied) counts as alive; a
// handle that cannot report its exit code also counts as alive, because
// refusing is safer than reclaiming a live holder's lock.
func pidAlive(pid int) bool {
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }() // query handle; nothing to report
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
