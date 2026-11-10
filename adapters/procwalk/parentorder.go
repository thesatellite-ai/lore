package procwalk

// createdFunc reads a process's creation time (any monotonic unit, larger
// is newer); ok is false when the time cannot be read (access denied, the
// process gone, not a valid pid). The Windows reader passes its
// GetProcessTimes lookup; tests pass fake times.
type createdFunc func(pid int) (at int64, ok bool)

// parentOlderBy reports whether ppid was created no later than pid, read
// through created (AGENT_IDENTITY_DESIGN 10.7: Windows reuses pids, so a
// recorded parent pid may now name a newer, unrelated process, and the
// walk must stop there). A time that cannot be read keeps the link: the
// walk then behaves as before the check, never worse.
//
// It lives in a file without a build tag so the decision is tested on
// every platform, although only the Windows reader needs it today.
func parentOlderBy(created createdFunc, pid, ppid int) bool {
	child, ok := created(pid)
	if !ok {
		return true
	}
	parent, ok := created(ppid)
	if !ok {
		return true
	}
	return parent <= child
}
