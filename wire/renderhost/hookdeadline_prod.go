//go:build !loretest

package renderhost

import "time"

// hookTimeoutOverride is never set in a release build: the hook deadline
// is part of the 6.9 contract (a hook never blocks the tool), and only a
// test build lets the environment move it (LORE_HOOK_TIMEOUT).
func hookTimeoutOverride() (time.Duration, bool) { return 0, false }
