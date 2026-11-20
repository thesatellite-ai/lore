package gitremote

// unreachable.go — one retry per command, not one per call (devco:H-6).
//
// A network call whose connection never opened gets one retry (Git.run),
// which saves a claim or a sync from one dropped ssh handshake. Offline, though, every call failed twice: a
// `lore sync` that fetched and then pushed the waiting live writes made
// four network round trips that all failed, two of them the same push.
// Once a call to a remote ended without ever connecting (its retry
// included), the remote is down for this command: later calls to it in
// the same process run once. A call that connects clears the mark, and the
// mark expires after unreachableFor so a long-running process retries again.

import (
	"strings"
	"sync"
	"time"
)

// unreachableFor is how long a remote whose call never connected stays
// known unreachable in this process (longer than one command, short
// enough that a long-running process soon retries a recovered link).
const unreachableFor = time.Minute

// reachMemo is the process's set of remotes known unreachable, keyed by
// remoteKey, with the time each mark ends. now is the clock (time.Now;
// a test sets its own).
type reachMemo struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time
}

// reach is the process-wide memo every network caller shares.
var reach = &reachMemo{until: map[string]time.Time{}, now: time.Now}

// remoteKey names one remote as reached from one local repository: its
// directory, the remote's name or url, and the route options that pick
// the account and host (two routes can reach two different hosts).
func remoteKey(dir, remote string, routeArgs, routeEnv []string) string {
	return strings.Join([]string{dir, remote, strings.Join(routeArgs, " "), strings.Join(routeEnv, " ")}, "\x00")
}

// retryDue reports whether a call to key that failed without connecting
// may have its one retry: false while this process already saw key never
// connect (markUnreachable) within unreachableFor.
func retryDue(key string) bool {
	reach.mu.Lock()
	defer reach.mu.Unlock()
	end, ok := reach.until[key]
	if !ok {
		return true
	}
	if !reach.now().Before(end) {
		delete(reach.until, key)
		return true
	}
	return false
}

// markUnreachable records that a call to key ended (after its retry, if
// it had one) without its connection ever opening.
func markUnreachable(key string) {
	reach.mu.Lock()
	defer reach.mu.Unlock()
	reach.until[key] = reach.now().Add(unreachableFor)
}

// markReached records that a call to key connected (it succeeded, or the
// remote answered with a refusal): later calls get their retry again.
func markReached(key string) {
	reach.mu.Lock()
	defer reach.mu.Unlock()
	delete(reach.until, key)
}
