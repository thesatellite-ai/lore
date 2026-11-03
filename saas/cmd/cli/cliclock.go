// cliclock.go — the one clock every lore-for-teams command reads
// (LORE_TEAM_SPEC §9.1 "Clock" seam). Lease expiry, max-age copies, the
// fetch cache and hlc stamps must agree on "now" within one process, and the
// team e2e suite moves each actor's clock with LORE_NOW (honoured only in
// `-tags loretest` builds). The implementation is the live rows' clock
// (live_clock.go), so there is exactly one.
package main

import "saas/pkg/aicoder/clock"

// cliClock returns the clock for this process.
func cliClock() clock.Clock { return liveClock() }
