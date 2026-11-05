// Package clock is a forwarding shell: the code moved to github.com/thesatellite-ai/lore/logic/clock
// (PASS3 P3-PORT-LIVE). Every exported name here is an alias or a thin
// wrapper, so the old importers need no edit; new code imports the root
// package directly.
package clock

import (
	"time"

	root "github.com/thesatellite-ai/lore/logic/clock"
)

// Clock forwards to the root package.
type Clock = root.Clock

// Fake forwards to the root package.
type Fake = root.Fake

// Real forwards to the root package.
type Real = root.Real

// Skewed forwards to the root package.
type Skewed = root.Skewed

// NewFake forwards to the root package.
func NewFake(start time.Time) *Fake { return root.NewFake(start) }
