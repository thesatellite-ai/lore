package main

import (
	"runtime"
	"testing"

	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// TestCapDoctorProcs: `lore doctor` runs on at most min(4, CPUs) cores; a
// lower GOMAXPROCS is kept, and any other command is left alone. Not
// parallel: GOMAXPROCS is the process's.
func TestCapDoctorProcs(t *testing.T) {
	prev := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })

	high := opsdoctor.MaxParallel() + 8
	for _, args := range [][]string{{"doctor"}, {"doctor", "--all"}, {"--json", "doctor", "run", "--all"}} {
		runtime.GOMAXPROCS(high)
		capDoctorProcs(args)
		if got := runtime.GOMAXPROCS(0); got != opsdoctor.MaxParallel() {
			t.Fatalf("lore %v: GOMAXPROCS %d, want %d", args, got, opsdoctor.MaxParallel())
		}
	}
	runtime.GOMAXPROCS(1)
	capDoctorProcs([]string{"doctor", "--all"})
	if got := runtime.GOMAXPROCS(0); got != 1 {
		t.Fatalf("a lower GOMAXPROCS was raised to %d", got)
	}
	runtime.GOMAXPROCS(high)
	capDoctorProcs([]string{"memory", "list"})
	if got := runtime.GOMAXPROCS(0); got != high {
		t.Fatalf("lore memory list: GOMAXPROCS %d, want it left at %d", got, high)
	}
}
