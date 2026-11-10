//go:build !windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// errLockBusy is the internal sentinel returned when flock fails with EWOULDBLOCK.
var errLockBusy = errors.New("selfupdate: upgrade lock busy")

// tryAcquire opens path and takes an exclusive, non-blocking flock(2).
func tryAcquire(path string) (*upgradeLock, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, cliFileMode)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockBusy
		}
		return nil, errors.Join(err, closeErr)
	}
	if err := writeContent(f); err != nil {
		// Closing the descriptor drops the flock.
		return nil, errors.Join(err, f.Close())
	}
	// Unlock explicitly before the close: a child forked by another goroutine
	// holds a copy of this descriptor until it execs, so a close alone can
	// leave the flock held after release (it belongs to the open file
	// description). LOCK_UN drops it for every copy.
	return &upgradeLock{path: path, file: f, owned: true, unlock: func() error { return syscall.Flock(fd, syscall.LOCK_UN) }}, nil
}
