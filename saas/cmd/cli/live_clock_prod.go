//go:build !loretest

package main

import "time"

// liveNowOverride is never set in production builds: LORE_NOW is a test
// seam (§1), and a release binary must not let an environment variable move
// lease expiry or max-age decisions.
func liveNowOverride() (time.Time, bool) { return time.Time{}, false }
