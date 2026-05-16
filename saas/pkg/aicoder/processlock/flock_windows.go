//go:build windows

package processlock

import (
	"errors"
	"os"
)

// Windows port deferred to v1.0+ per canonical spec; minimal stub keeps the
// package buildable on Windows for go vet / cross-compile checks.

var errLockBusy = errors.New("processlock: lock busy")

func tryAcquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, errLockBusy
		}
		return nil, err
	}
	if err := writeContent(f); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &Lock{path: path, file: f, owned: true}, nil
}

func pidAlive(_ int) bool {
	// Conservative on Windows — assume alive (refuse rather than reclaim).
	return true
}
