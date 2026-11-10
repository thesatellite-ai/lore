//go:build unix

package gitleaks

import (
	"syscall"
	"time"
)

// cpuClock is the CPU time (user + system) this test process has used so
// far. The budget is timed on it, not on the wall clock: a busy machine
// (task check runs many suites in parallel) stretches wall time while the
// scanner waits for a core, but not the CPU time the scan itself costs, so
// the budget stays a check of the code and not of the load. TestScanBudgets
// is not parallel, so no other test of the package runs while it is timed.
// A failed Getrusage falls back to the wall clock (wallClock).
func cpuClock() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return wallClock()
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
