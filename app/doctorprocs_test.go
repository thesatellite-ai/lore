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

// TestPaceDoctorGC (bug hunt H-277, devco:D-1015): `lore doctor` collects
// at the Go default pace, not the CLI's gcPercent, so its memory stays
// near what it keeps; GOGC set in the environment wins, and any other
// command keeps the CLI's pace. Not parallel: the GC percent is the
// process's.
func TestPaceDoctorGC(t *testing.T) {
	prev := debug.SetGCPercent(gcPercent)
	t.Cleanup(func() { debug.SetGCPercent(prev) })
	none := func(string) string { return "" }

	for _, args := range [][]string{{"doctor"}, {"--json", "doctor", "run", "--all"}} {
		debug.SetGCPercent(gcPercent)
		if !paceDoctorGC(args, none) {
			t.Fatalf("lore %v: GC pace not set", args)
		}
		if got := debug.SetGCPercent(gcPercent); got != doctorGCPercent {
			t.Fatalf("lore %v: GC percent %d, want %d", args, got, doctorGCPercent)
		}
	}
	debug.SetGCPercent(gcPercent)
	if paceDoctorGC([]string{"memory", "list"}, none) || debug.SetGCPercent(gcPercent) != gcPercent {
		t.Fatal("lore memory list: GC pace changed, want the CLI's")
	}
	debug.SetGCPercent(gcPercent)
	if paceDoctorGC([]string{"doctor"}, func(k string) string { return map[string]string{"GOGC": "300"}[k] }) {
		t.Fatal("GOGC in the environment overridden")
	}
}
