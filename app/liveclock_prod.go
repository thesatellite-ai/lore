//go:build !loretest

package app

import "time"

// liveNowOverride is never set in a release build: LORE_NOW is a test seam,
// and a release binary must not let the environment move time.
func liveNowOverride() (time.Time, bool) { return time.Time{}, false }
