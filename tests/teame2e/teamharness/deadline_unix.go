//go:build !windows

package teamharness

import (
	"os"
	"syscall"
)

// quitProcess asks p to print its goroutines and exit (SIGQUIT: the Go
// runtime's dump).
func quitProcess(p *os.Process) { _ = p.Signal(syscall.SIGQUIT) }
