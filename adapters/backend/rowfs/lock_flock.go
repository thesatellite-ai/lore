//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package rowfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// tryLock opens path and takes an exclusive, non-blocking flock(2). flock
// belongs to the open file description, so two handles in one process
// exclude each other as two processes do.
func tryLock(path string) (*fileLock, error) {
	if err := refuseLinkedLock(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, fileMode)
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
	// Unlock explicitly before the close: the flock belongs to the open file
	// description, and a child forked by another goroutine holds a copy of
	// this descriptor until it execs, so closing only our copy can leave the
	// lock held after Release. LOCK_UN drops it for every copy at once.
	l := &fileLock{file: f, unlock: func() error { return syscall.Flock(fd, syscall.LOCK_UN) }}
	if err := writeHolder(f); err != nil {
		return nil, errors.Join(err, l.release())
	}
	return l, nil
}
