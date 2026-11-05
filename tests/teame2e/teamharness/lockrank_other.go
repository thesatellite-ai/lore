//go:build !linux

package teamharness

// CheckLockRanks needs /proc/locks (lockrank_linux.go); elsewhere the
// suite-wide switch LORE_TEAM_LOCK_RANKS=1 fails loudly rather than
// reporting a check that never ran.
func (w *World) CheckLockRanks(func(LockOrder) bool) {
	w.T.Fatal("LORE_TEAM_LOCK_RANKS=1 needs /proc/locks: run the lock order check on Linux")
}

// LockOrder is one observed acquisition order (lockrank_linux.go).
type LockOrder struct {
	PID         int
	First, Then string
}
