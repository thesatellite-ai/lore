//go:build journeys && !windows

// Package journeys is lore's real-user journey suite: one person or team
// using lore over weeks and months, in order, the way people do, through the
// real binary. Every step is followed by the shared invariants (invariants.go)
// on every machine of the World, so an invariant added once applies to every
// journey at once. How to add a persona, a step or an invariant:
// tests/journeys/README.md.
//
// The harness stands on tests/teame2e/teamharness (fake GitHub remotes reached
// through real URLs, one HOME per machine, the LORE_NOW test clock, offline
// machines, a git shim that can kill lore at a chosen git step) and adds what a
// journey needs on top: one clock for the whole World (AdvanceDays), Personas
// that own Machines (NewLaptop), machine actions (Offline, SleepMidPush),
// Step, the invariant registry and per-journey Facts.
//
// Placeholders only: every name, email and URL here is made up.
package journeys

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// JourneyStart is day 1 of every journey: a fixed instant so a journey reads
// the same on every run (Monday 09:00 UTC).
var JourneyStart = time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)

// World is one isolated universe for one journey: the fake GitHub, every
// persona and machine, one clock, and the facts the journey has established.
type World struct {
	*teamharness.World
	T        *testing.T
	now      time.Time
	personas []*Persona
	machines []*Machine
	facts    []Fact
	step     string
}

// NewWorld makes a World on the suite's binary (TestMain: JOURNEYS_LORE or a
// loretest build) with the clock at JourneyStart. The binary must honour
// LORE_NOW, or AdvanceDays would be a lie; NewWorld fails at once if not.
func NewWorld(t *testing.T) *World {
	t.Helper()
	tw := teamharness.New(t, loreBin)
	tw.RequireTestClock()
	return &World{World: tw, T: t, now: JourneyStart}
}

// Now is the World's clock.
func (w *World) Now() time.Time { return w.now }

// Advance moves the World's clock forward by d on every machine (a machine
// made later starts at the new time).
func (w *World) Advance(d time.Duration) {
	w.T.Helper()
	if d < 0 {
		w.T.Fatalf("journey clock never goes back (Advance %v)", d)
	}
	w.now = w.now.Add(d)
	for _, m := range w.machines {
		m.SetNow(w.now)
	}
}

// AdvanceDays moves the World's clock forward n days.
func (w *World) AdvanceDays(n int) { w.Advance(time.Duration(n) * 24 * time.Hour) }

// Machines is every machine of every persona, in the order they were made.
func (w *World) Machines() []*Machine { return append([]*Machine(nil), w.machines...) }

// Personas is every persona, in the order they were made.
func (w *World) Personas() []*Persona { return append([]*Persona(nil), w.personas...) }

// CurrentStep is the name of the step running now ("" outside a step).
func (w *World) CurrentStep() string { return w.step }
