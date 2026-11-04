// Package hlc is the hybrid logical clock stamped on every row edit
// ("_hlc", STORE_SERVER_SPEC 4.1): wall time, a counter and the writing
// actor. It orders two edits of one row even when the writers' clocks
// disagree, because a writer never issues a value below one it has seen.
//
// The text form is the one v0.1.17 live rows already carry
// (saas/pkg/aicoder/live/hlc.go): "2026-11-04T10:42:07.123Z/0003/act_…".
// Fixed widths keep string order equal to value order.
//
// Invariant: a parsed or issued HLC has Wall in UTC truncated to the
// millisecond, so String and Parse round-trip byte for byte.
package hlc

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// HLC is one clock value. The zero value is "never stamped" and sorts before
// every stamped value.
type HLC struct {
	Wall    time.Time
	Counter uint32
	Actor   ids.ID[ids.Actor]
}

// Format facts of the text form; changing any of them rewrites every row
// file, so each is a format-version decision.
const (
	wallLayout   = "2006-01-02T15:04:05.000Z"
	precision    = time.Millisecond
	counterWidth = 4
	sep          = "/"
	parts        = 3
)

// String renders the text form; the zero HLC renders as "".
func (h HLC) String() string {
	if h.IsZero() {
		return ""
	}
	c := strconv.FormatUint(uint64(h.Counter), 10)
	if pad := counterWidth - len(c); pad > 0 {
		c = strings.Repeat("0", pad) + c
	}
	return h.Wall.UTC().Format(wallLayout) + sep + c + sep + h.Actor.String()
}

// IsZero reports the never-stamped value.
func (h HLC) IsZero() bool { return h.Wall.IsZero() && h.Counter == 0 && h.Actor.IsZero() }

// Parse reads the canonical text form; "" is the zero value. Anything that
// String would not print exactly (other time zones, extra digits, a short
// counter) is refused, so a parsed value always re-encodes to the same bytes.
func Parse(s string) (HLC, error) {
	if s == "" {
		return HLC{}, nil
	}
	p := strings.Split(s, sep)
	if len(p) != parts {
		return HLC{}, invalid(s, "want wall/counter/actor")
	}
	wall, err := time.Parse(wallLayout, p[0])
	if err != nil {
		return HLC{}, invalid(s, "wall time")
	}
	n, err := strconv.ParseUint(p[1], 10, 32)
	if err != nil {
		return HLC{}, invalid(s, "counter")
	}
	var actor ids.ID[ids.Actor]
	if p[2] != "" {
		if actor, err = ids.Parse[ids.Actor](p[2]); err != nil {
			return HLC{}, invalid(s, "actor")
		}
	}
	h := HLC{Wall: wall.UTC(), Counter: uint32(n), Actor: actor}
	if h.String() != s {
		return HLC{}, invalid(s, "not in canonical form")
	}
	return h, nil
}

// invalid builds the parse error; the input is short and holds no secret
// (an hlc is a time, a number and an actor id).
func invalid(s, why string) error {
	return errs.New(errs.InvalidInput, "hlc: "+strconv.Quote(s)+": "+why)
}

// Compare orders by wall, then counter, then actor id: a total order, so
// every machine agrees which edit is newer. Returns -1, 0 or 1.
func (h HLC) Compare(o HLC) int {
	switch {
	case h.Wall.Before(o.Wall):
		return -1
	case h.Wall.After(o.Wall):
		return 1
	case h.Counter < o.Counter:
		return -1
	case h.Counter > o.Counter:
		return 1
	}
	return strings.Compare(h.Actor.String(), o.Actor.String())
}

// Max returns the larger of a and b.
func Max(a, b HLC) HLC {
	if a.Compare(b) >= 0 {
		return a
	}
	return b
}

// Next is the pure stamping rule: the stamp for actor at wall-clock now that
// is strictly greater than prev. It is now when now is ahead of prev's wall,
// else prev's wall with the counter raised (and the wall moved on by one
// millisecond if the counter would wrap).
func Next(prev HLC, now time.Time, actor ids.ID[ids.Actor]) HLC {
	wall := now.UTC().Truncate(precision)
	if wall.After(prev.Wall) {
		return HLC{Wall: wall, Actor: actor}
	}
	if prev.Counter == ^uint32(0) {
		return HLC{Wall: prev.Wall.Add(precision), Actor: actor}
	}
	return HLC{Wall: prev.Wall, Counter: prev.Counter + 1, Actor: actor}
}

// FromUpdate is the v1 upcast rule (spec 12): a row written before hlc
// existed gets updated_at truncated to the millisecond, counter 0 and its
// updated_by actor (zero when unknown).
func FromUpdate(updatedAt time.Time, updatedBy ids.ID[ids.Actor]) HLC {
	if updatedAt.IsZero() {
		return HLC{Actor: updatedBy}
	}
	return HLC{Wall: updatedAt.UTC().Truncate(precision), Actor: updatedBy}
}

// Clock issues stamps for one actor. It remembers the last value it issued
// so two stamps from one process never tie, and Now takes every value the
// caller has seen (the row being edited, a merged remote row) so a stamp is
// always above them whatever the local wall clock says.
//
// Safe for concurrent use. The wall clock is injected so tests and replays
// control time; nothing reads time.Now behind the caller's back.
type Clock struct {
	actor ids.ID[ids.Actor]
	wall  func() time.Time

	mu   sync.Mutex
	last HLC
}

// NewClock builds a Clock for actor reading wall (pass time.Now in
// production).
func NewClock(actor ids.ID[ids.Actor], wall func() time.Time) *Clock {
	return &Clock{actor: actor, wall: wall}
}

// Now issues a stamp strictly greater than every value in seen and every
// value this Clock issued before.
func (c *Clock) Now(seen ...HLC) HLC {
	c.mu.Lock()
	defer c.mu.Unlock()
	base := c.last
	for _, s := range seen {
		base = Max(base, s)
	}
	c.last = Next(base, c.wall(), c.actor)
	return c.last
}
