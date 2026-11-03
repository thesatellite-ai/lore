// live_clock.go — the clock and git runner live rows use. Production builds
// use the wall clock; test builds (tag loretest) honour LORE_NOW
// (live_clock_loretest.go), so max-age and fetch-cache scenarios can move
// time without sleeping (§1 "Environment variables": test builds only).
package main

import (
	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitref"
)

// liveClock is the clock every live decision uses.
func liveClock() clock.Clock {
	if t, ok := liveNowOverride(); ok {
		return clock.NewFake(t)
	}
	return clock.Real{}
}

// liveGit is the git runner of live refs; with --trace / LORE_TRACE=1 it
// prints every git command and its timing.
func liveGit() gitref.Git {
	if liveTraceOn() {
		return tracingGit{inner: gitref.Exec{}}
	}
	return gitref.Exec{}
}
