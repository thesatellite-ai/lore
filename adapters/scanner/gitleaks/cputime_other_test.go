//go:build !unix

package gitleaks

import "time"

// cpuClock falls back to the wall clock where the process CPU time is not
// read through syscall.Getrusage (Windows); see cputime_unix_test.go.
func cpuClock() time.Duration { return wallClock() }
