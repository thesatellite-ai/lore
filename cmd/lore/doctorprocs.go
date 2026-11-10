package main

import (
	"runtime"

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
