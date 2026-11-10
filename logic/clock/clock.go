// Package clock is the time seam for code whose decisions depend on "now":
// lease expiry, max-age copies, fetch caching, hybrid logical clocks and
// commit dates.
//
// Why it exists: a decision that calls time.Now() directly can only be
// tested by sleeping, and a flaky sleep-based test proves nothing about the
// edge it was meant to pin (a lease expiring one second early or late).
// Every such caller takes a Clock instead; production passes Real{}, tests
// pass a *Fake and move it explicitly.
//
// The package holds no state of its own: Real reads the system clock and a
// Fake's time lives only in the value the test created.
package clock

import (
	"sync"
	"time"
)

// Clock reports the current time.
//
// Invariant: implementations are safe for concurrent use, because one clock
// is shared by every goroutine of a command (a heartbeat daemon and the
// command that started it read the same clock).
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// Real is the system clock.
type Real struct{}

// Now returns time.Now().
func (Real) Now() time.Time { return time.Now() }

// Fake is a settable clock for tests. Its zero value is not useful; build
// one with NewFake so the starting instant is explicit in every test.
//
// Invariant: time only moves when the test calls Set or Advance, so two
// reads with no call in between return the same instant.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a Fake reading start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

// Now returns the fake's current instant.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the fake to t (backwards is allowed: tests of clock skew need
// it).
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

// Advance moves the fake forward by d (a negative d moves it back) and
// returns the new instant.
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}

// Skewed is a Clock that reads another clock shifted by a fixed offset. It
// models a machine whose clock is off, so a test can run two agents on one
// Fake with one of them minutes ahead.
type Skewed struct {
	// Base is the clock being shifted.
	Base Clock
	// Offset is added to every reading (negative = behind).
	Offset time.Duration
}

// Now returns Base.Now() + Offset.
func (s Skewed) Now() time.Time { return s.Base.Now().Add(s.Offset) }
