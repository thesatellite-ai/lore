package app

import "github.com/thesatellite-ai/lore/logic/clock"

// liveClock is the clock of row stamps (wire.Options.Wall) and live decisions:
// the wall clock, or LORE_NOW in a test build (liveclock_loretest.go).
func liveClock() clock.Clock {
	if t, ok := liveNowOverride(); ok {
		return clock.NewFake(t)
	}
	return clock.Real{}
}
