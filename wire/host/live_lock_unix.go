//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package host

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// tryLiveLock takes an exclusive, non-blocking flock(2) on path.
func tryLiveLock(path string) (*liveLock, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, liveLockMode)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLiveLockBusy
		}
		return nil, errors.Join(err, closeErr)
	}
	return &liveLock{f: f, unlock: func() error { return syscall.Flock(fd, syscall.LOCK_UN) }}, nil
}
