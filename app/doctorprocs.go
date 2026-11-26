package app

import (
	"runtime"
	"runtime/debug"

	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// doctorCommand is the command whose process is capped to
// opsdoctor.MaxParallel cores.
const doctorCommand = "doctor"

// capDoctorProcs caps the Go scheduler to opsdoctor.MaxParallel (min(4,
// CPUs)) for `lore doctor`, before anything starts: its probes already run
// at most that many at once (wire/setupprobe.go), and the cap keeps the
// rest of the run (cache reads, the garbage collector) on as many cores.
// A lower GOMAXPROCS the person set is kept. Other commands are left as
// they are: a long-lived `lore api` or a store rebuild may use every core.
func capDoctorProcs(args []string) {
	if commandName(args) != doctorCommand {
		return
	}
	if n := opsdoctor.MaxParallel(); runtime.GOMAXPROCS(0) > n {
		runtime.GOMAXPROCS(n)
	}
}

// doctorGCPercent is `lore doctor`'s GOGC: the Go default, not the CLI's
// gcPercent. Why (bug hunt H-277, devco:D-1015): gcPercent lets the heap
// grow five times past what a collection keeps, which suits a command of a
// tenth of a second; doctor runs for seconds over every row of every
// attached store, so at 100k commits it kept ~150 MB and peaked at ~1 GB
// RSS. At the Go default it peaks at about twice what it keeps, for ~8%
// more CPU and the same wall time.
const doctorGCPercent = 100

// doctorMemLimit is `lore doctor`'s soft memory limit: the CLI's gcLimit
// (2 GiB) lowered. Why (bug hunt H-278, devco:D-1016): at the Go default
// pace doctor's heap still grew to about twice what it kept (~210 MB kept
// at 100k commits, ~400 MB heap, ~480 MB RSS) because its checks allocate
// fast between collections; under this limit the collector runs earlier
// once the heap nears it (~340 MB RSS, same wall time). It is a floor: a
// store whose doctor keeps more than this gets a limit a quarter above
// what it keeps (followLiveHeap, bug hunt H-280: under the fixed limit a
// 1.26M-row store's doctor spent 45+ minutes collecting).
const doctorMemLimit = 256 << 20

// paceDoctorGC sets doctorGCPercent and doctorMemLimit for `lore doctor`
// unless the environment chose its own GOGC or GOMEMLIMIT (the person's
// tuning wins, as with gcDefaults). It reports what it set, for the test.
func paceDoctorGC(args []string, getenv func(string) string) (percent bool, limit bool) {
	if commandName(args) != doctorCommand {
		return false, false
	}
	if getenv("GOGC") == "" {
		debug.SetGCPercent(doctorGCPercent)
		percent = true
	}
	if getenv("GOMEMLIMIT") == "" {
		followLiveHeap(doctorMemLimit) // gcfollow.go: a big store's doctor gets room above what it keeps (H-280)
		limit = true
	}
	return percent, limit
}
