// Package testtime scales the test time limits that are about how long a
// whole run takes, so they hold on any machine.
//
// Why: every go test -timeout, suite deadline and "wait for this slow
// process" limit was tuned on a 48-core Linux gate server. A 10-core laptop
// runs each step 3-5x slower, and the suites died at their 15/20/40/60 min
// limits with nothing wrong. One factor, LORE_TEST_TIME_SCALE, stretches all
// of them; on the gate server it is 1, so the limits there are unchanged.
//
// Product speed bars ("finishes within N") go through SpeedBar: unchanged at
// factor 1, stretched on slower machines (D-417).
//
// What does NOT scale: a timeout that is the behaviour under test (a lock
// wait, a fetch timeout, back-off, a TTL, a kill deadline). Scaling those
// would hide the very failure they test.
//
// The shell side (Taskfile -timeout values, gate scripts) is
// tests/lib/testtime.sh; it computes the same factor with the same rules, and
// TestShellHelperAgrees keeps the two in step. docs/DEVELOPMENT.md "Test time
// limits" is the reference for people.
package testtime

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// EnvVar names the variable that sets the factor by hand: a number >= 1
// (for example 4 or 2.5). Unset or empty means "compute it from the machine".
const EnvVar = "LORE_TEST_TIME_SCALE"

// Reference is the core count the limits were tuned on (the gate server);
// a machine with at least this many cores gets factor 1.
const Reference = 48

// MaxCoreFactor caps the core-count part of the factor, so a 2-core runner
// waits 6x, not 24x, before a real hang is reported.
const MaxCoreFactor = 6.0

// DarwinFactor is the extra stretch on macOS: per core it ran the suites
// about 1.5x slower than Linux (process start, file system, git).
const DarwinFactor = 1.5

// InvalidScaleError reports a LORE_TEST_TIME_SCALE value that is not a
// number >= 1. Why typed: a test (and the shell helper's agreement test) can
// tell a bad setting from any other failure.
type InvalidScaleError struct {
	Value string
}

func (e *InvalidScaleError) Error() string {
	return fmt.Sprintf("%s=%q: want a number >= 1 (for example %s=4), or unset it to compute it from the machine", EnvVar, e.Value, EnvVar)
}

// FactorFor computes the factor for an environment value, a core count and
// a GOOS. Why exported with every input explicit: the guard tests inject a
// 48-core Linux or a 10-core Mac without being on one.
func FactorFor(env string, nproc int, goos string) (float64, error) {
	if env != "" {
		f, err := strconv.ParseFloat(env, 64)
		if err != nil || f < 1 || f != f { // f != f: NaN
			return 0, &InvalidScaleError{Value: env}
		}
		return f, nil
	}
	f := 1.0
	if nproc > 0 && nproc < Reference {
		f = float64(Reference) / float64(nproc)
	}
	if f > MaxCoreFactor {
		f = MaxCoreFactor
	}
	if goos == "darwin" {
		f *= DarwinFactor
	}
	return f, nil
}

var (
	once      sync.Once
	factor    float64
	factorErr error
)

// Factor is this machine's factor: LORE_TEST_TIME_SCALE when set, else the
// computed one. It panics on an invalid LORE_TEST_TIME_SCALE: a test run with
// a broken setting should stop at once with the reason, not run with limits
// nobody asked for.
func Factor() float64 {
	once.Do(func() {
		factor, factorErr = FactorFor(os.Getenv(EnvVar), runtime.NumCPU(), runtime.GOOS)
	})
	if factorErr != nil {
		panic(factorErr)
	}
	return factor
}

// Scale stretches d, a limit on how long a whole run or a slow process may
// take, by this machine's factor. Use it for every such limit in a test
// harness; never for a timeout that is itself under test.
func Scale(d time.Duration) time.Duration {
	return ScaleBy(d, Factor())
}

// SpeedBar stretches d, a product speed bar ("doctor --all finishes within
// 30 s", "a 20 MB binary is checked in under 2 s"), by this machine's
// factor. At factor 1 (the gate server) the bar is exactly as written, so
// it still catches a real slowdown there; on a slower machine it stretches
// like every run limit, so a laptop does not fail it for being a laptop
// (owner decision D-417, option A; the machine's own command speed is
// measured separately). Why its own name: TestSpeedBarsScale tells a speed
// bar from a behaviour bound (a lock wait, a kill deadline, a timeout under
// test), which never scales.
func SpeedBar(d time.Duration) time.Duration {
	return ScaleBy(d, Factor())
}

// ScaleBy stretches d by f (rounded to the second for limits of a second or
// more, so messages read "6m0s", not "6m0.000123s"). Why separate: the
// guard test applies a known factor to a sample deadline.
func ScaleBy(d time.Duration, f float64) time.Duration {
	if f <= 1 {
		return d
	}
	s := time.Duration(float64(d) * f)
	if s >= time.Second {
		s = s.Round(time.Second)
	}
	return s
}
