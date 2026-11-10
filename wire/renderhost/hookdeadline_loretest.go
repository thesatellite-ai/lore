//go:build loretest

package renderhost

import (
	"os"
	"time"
)

// envHookTimeout moves the 6.9 hook deadline in test builds only (a Go
// duration, e.g. "2m"): an end-to-end test that asserts a hook's answer
// sets it, so a machine saturated by the gate's other suites cannot turn
// the answer into the deadline's silent nothing (TestRenderSessionStartHook
// failed that way under load, 2026-11-09).
const envHookTimeout = "LORE_HOOK_TIMEOUT"

// hookTimeoutOverride reads LORE_HOOK_TIMEOUT; unset, unparsable or not
// positive leaves the deadline as it is.
func hookTimeoutOverride() (time.Duration, bool) {
	d, err := time.ParseDuration(os.Getenv(envHookTimeout))
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}
