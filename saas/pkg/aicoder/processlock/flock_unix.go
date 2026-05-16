//go:build !windows

package processlock

import (
	"errors"
	"os"
	"syscall"
)

// errLockBusy is the internal sentinel returned when flock fails with EWOULDBLOCK.
var errLockBusy = errors.New("processlock: flock busy")

// tryAcquire opens path O_CREATE|O_RDWR and attempts an exclusive non-blocking flock.
func tryAcquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockBusy
		}
		return nil, err
	}
	if err := writeContent(f); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	return &Lock{path: path, file: f, owned: true}, nil
}

// pidAlive returns true if pid is a running process.
// On Unix, kill(pid, 0) returns nil if the process exists (and we can signal it),
// returns ESRCH if it doesn't.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	return true
}
