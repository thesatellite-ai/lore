// Package locktrace is the lock trace seam of the test builds: every lore
// lock (the live lock L, the migration lock M, the rowfs store and config
// locks) reports when it was taken, released, or found busy, so the team
// e2e suite checks lock order and lock waits from the events themselves
// (tests/teame2e TestLockOrderRanks, TestAPIParity_CLIBesideRunningAPI)
// instead of sampling /proc/locks or timing a command, which machine load
// defeats.
//
// A release build (no `loretest` tag) compiles every call to an empty
// function (trace_release.go): an environment variable can never make a
// real lore write a file. A loretest build appends one line per event to
// the file named by LORE_TEST_LOCK_TRACE (trace_loretest.go):
//
//	<pid>\t<process token>\t<event>\t<unix nanos>\t<path>
//
// Events of one process are in program order (one write(2) per line on an
// O_APPEND file). "taken" is written once the lock is held and "released"
// before it is dropped, so across processes a release of the previous
// holder always comes before the next holder's take in the file.
package locktrace

// Event is what happened to a lock.
type Event string

// The events a lock reports.
const (
	// Taken: the process now holds the lock.
	Taken Event = "taken"
	// Released: the process is about to drop the lock.
	Released Event = "released"
	// Busy: the process found the lock held by another and starts waiting
	// (reported once per acquisition, on the first busy try).
	Busy Event = "busy"
)

// EnvTrace names the trace file (loretest builds only).
const EnvTrace = "LORE_TEST_LOCK_TRACE"

// Lock reports ev on the lock file at path.
func Lock(ev Event, path string) { record(ev, path) }
