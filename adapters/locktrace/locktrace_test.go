package locktrace

import "testing"

// TestLockWithoutTraceFile: with no LORE_TEST_LOCK_TRACE a report is a
// no-op in every build (it must never fail or block a lock).
func TestLockWithoutTraceFile(t *testing.T) {
	t.Setenv(EnvTrace, "")
	Lock(Taken, "/nowhere/live.lock")
	Lock(Released, "/nowhere/live.lock")
}
