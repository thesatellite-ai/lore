//go:build windows

package host

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// The locked range is one byte far past any content (rowfs lock_windows.go).
const (
	liveLockOffsetHigh = 0x7FFFFFFF
	liveLockLen        = 1
)

// tryLiveLock takes an exclusive, non-blocking LockFileEx lock on path.
func tryLiveLock(path string) (*liveLock, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, liveLockMode)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ov := func() *windows.Overlapped { return &windows.Overlapped{OffsetHigh: liveLockOffsetHigh} }
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(h, flags, 0, liveLockLen, 0, ov()); err != nil {
		closeErr := f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errLiveLockBusy
		}
		return nil, errors.Join(err, closeErr)
	}
	return &liveLock{f: f, unlock: func() error { return windows.UnlockFileEx(h, 0, liveLockLen, 0, ov()) }}, nil
}
