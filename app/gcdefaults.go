package app

import (
	"os"
	"runtime/debug"
)

// Most lore commands live for a tenth of a second with a heap of a few
// tens of MB, and Go's default (a collection each time the heap doubles,
// mark workers on every core) spent about a fifth of such a process's CPU
// in the collector (fastgate:G1a). A gate or ten agents run thousands of
// these at once, so that CPU is the wall time. gcPercent lets the heap grow
// five times before a collection; gcLimit is a soft cap so a big job (a
// 100k-row refresh) still collects before it grows past it.
const (
	gcPercent = 400
	gcLimit   = 2 << 30 // 2 GiB
)

// applyGCDefaults sets the collector's pace for a short-lived CLI process,
// unless the environment chose its own (GOGC, GOMEMLIMIT win: the Go
// runtime already read them). It reports what it set, for the test.
func applyGCDefaults(getenv func(string) string) (percent bool, limit bool) {
	if getenv("GOGC") == "" {
		debug.SetGCPercent(gcPercent)
		percent = true
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(gcLimit)
		limit = true
	}
	return percent, limit
}

// gcDefaults applies applyGCDefaults to the process environment.
func gcDefaults() { applyGCDefaults(os.Getenv) }
