package app

import (
	"runtime/debug"
	"testing"
)

// TestGCDefaults (fastgate:G1a): a lore process collects at gcPercent with
// a gcLimit soft cap, and an environment that sets GOGC or GOMEMLIMIT
// keeps its own value (the gate's LORE_E2E_GOENV, a user's tuning).
func TestGCDefaults(t *testing.T) {
	// Not parallel: it sets the process-wide collector settings.
	prevPct := debug.SetGCPercent(100)
	prevLim := debug.SetMemoryLimit(-1)
	defer func() {
		debug.SetGCPercent(prevPct)
		debug.SetMemoryLimit(prevLim)
	}()
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	if p, l := applyGCDefaults(getenv); !p || !l {
		t.Fatalf("no env: set percent=%v limit=%v, want both", p, l)
	}
	if got := debug.SetGCPercent(100); got != gcPercent {
		t.Errorf("GC percent = %d, want %d", got, gcPercent)
	}
	if got := debug.SetMemoryLimit(-1); got != gcLimit {
		t.Errorf("memory limit = %d, want %d", got, gcLimit)
	}

	env["GOGC"], env["GOMEMLIMIT"] = "off", "512MiB"
	debug.SetGCPercent(-1)
	if p, l := applyGCDefaults(getenv); p || l {
		t.Fatalf("env set: overrode percent=%v limit=%v", p, l)
	}
	if got := debug.SetGCPercent(-1); got != -1 {
		t.Errorf("GOGC=off overridden: percent = %d", got)
	}
}
