//go:build !windows

package wire

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid runs on this machine: signal 0 probes it
// (EPERM: it runs under another user).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
