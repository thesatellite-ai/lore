package app

import (
	"runtime"
	"runtime/debug"
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

// TestPaceDoctorGC (bug hunt H-277, devco:D-1015; H-278, devco:D-1016):
// `lore doctor` collects at the Go default pace under doctorMemLimit, not
// the CLI's gcPercent and gcLimit, so its memory stays near what it keeps;
// GOGC or GOMEMLIMIT set in the environment wins, and any other command
// keeps the CLI's pace. Not parallel: the GC settings are the process's.
func TestPaceDoctorGC(t *testing.T) {
	prev := debug.SetGCPercent(gcPercent)
	prevLimit := debug.SetMemoryLimit(gcLimit)
	t.Cleanup(func() { debug.SetGCPercent(prev); debug.SetMemoryLimit(prevLimit) })
	none := func(string) string { return "" }
	reset := func() { debug.SetGCPercent(gcPercent); debug.SetMemoryLimit(gcLimit) }

	for _, args := range [][]string{{"doctor"}, {"--json", "doctor", "run", "--all"}} {
		reset()
		if p, l := paceDoctorGC(args, none); !p || !l {
			t.Fatalf("lore %v: GC pace set %v, memory limit set %v", args, p, l)
		}
		if got := debug.SetGCPercent(gcPercent); got != doctorGCPercent {
			t.Fatalf("lore %v: GC percent %d, want %d", args, got, doctorGCPercent)
		}
		if got := debug.SetMemoryLimit(-1); got != doctorMemLimit {
			t.Fatalf("lore %v: memory limit %d, want %d", args, got, doctorMemLimit)
		}
	}
	reset()
	if p, l := paceDoctorGC([]string{"memory", "list"}, none); p || l || debug.SetGCPercent(gcPercent) != gcPercent || debug.SetMemoryLimit(-1) != gcLimit {
		t.Fatal("lore memory list: GC pace changed, want the CLI's")
	}
	reset()
	env := map[string]string{"GOGC": "300", "GOMEMLIMIT": "1GiB"}
	if p, l := paceDoctorGC([]string{"doctor"}, func(k string) string { return env[k] }); p || l {
		t.Fatal("GOGC or GOMEMLIMIT in the environment overridden")
	}
	if debug.SetMemoryLimit(-1) != gcLimit {
		t.Fatal("GOMEMLIMIT in the environment: memory limit changed")
	}
}
