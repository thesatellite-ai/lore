// Package opclock is the one place an op reads "now".
//
// Every time an op stamps or compares (archived_at, taken_at, done_at,
// tx_at, a reminder's due check, a task's lease) must be the time the
// engine stamps the row's HLC and created_at with, so a surface that fixes
// the clock (the CLI's LORE_NOW in loretest builds, a test's fake wall)
// gets one consistent "now" across the row. The Env an op is given offers
// it through the optional Clocker upgrade (wire.App.Now, the engine's wall
// clock); an Env without it falls back to the system clock, so no Env is
// forced to grow a method it has no use for.
package opclock

import (
	"time"

	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Clocker is the optional Env upgrade that supplies the wall clock.
type Clocker interface {
	// Now is the current time as the engine sees it.
	Now() time.Time
}

// Now returns env's clock when env is a Clocker, else time.Now. A nil
// Clocker value (a typed nil pointer) is the caller's bug and panics as
// any nil method call would; it is never silently replaced.
func Now[E any](env E) time.Time {
	if c, ok := upgrade.As[Clocker](env); ok {
		return c.Now()
	}
	return time.Now()
}
