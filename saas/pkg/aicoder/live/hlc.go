// Package live implements live rows (LORE_TEAM_SPEC.md §2, §3.3, F1–F6,
// F8): rows shared with everyone at once on one hidden ref per repo
// (refs/lore/live, or refs/lore/live/<branch> in a store), with one folder
// per lore project inside the ref's tree.
//
// The package is a mechanism over gitref (local refs, trees, commits) and
// gitremote (fetch and push): it never decides policy such as which rows
// are live (that is rowroute) or which org a repo belongs to. Every
// decision is a pure function (ShouldFetch, ShouldLand, Confirm, MergeTree,
// PlanReapply) with an executor around it, so the decisions are table
// tested without git and the executors are tested on real git in temp
// folders.
package live

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HLC is a hybrid logical clock value (§3.3): wall time, a counter and the
// writer. It orders two edits of one row even when the writers' clocks
// disagree (F4): a writer never issues a value below the one it edited, so
// "edited after seeing X" always sorts after X whatever the laptop clock
// says.
//
// Invariant: Wall is UTC and truncated to hlcWallPrecision, so the string
// form round-trips exactly and sorts like the value.
type HLC struct {
	Wall    time.Time
	Counter uint32
	Actor   string
}

// hlcWallLayout is the fixed-width wall part of the string form. Fixed
// width (always three fractional digits) keeps string order equal to time
// order, so a plain string compare of two hlc values is also correct.
const hlcWallLayout = "2006-01-02T15:04:05.000Z"

// hlcWallPrecision is what the wall part keeps; finer digits are dropped so
// String/ParseHLC round-trip.
const hlcWallPrecision = time.Millisecond

// hlcCounterWidth is the zero-padded width of the counter ("0003"): fixed
// width again keeps string order equal to value order for every counter a
// real row reaches (a row edited 10,000 times within one millisecond of a
// skewed clock is not a real case; Compare stays correct beyond it).
const hlcCounterWidth = 4

// hlcSep separates the three parts.
const hlcSep = "/"

// hlcParts is how many parts the string form has.
const hlcParts = 3

// String renders "2026-11-02T10:04:11.000Z/0003/act_3f9c".
func (h HLC) String() string {
	if h.IsZero() {
		return ""
	}
	return h.Wall.UTC().Format(hlcWallLayout) + hlcSep +
		fmt.Sprintf("%0*d", hlcCounterWidth, h.Counter) + hlcSep + h.Actor
}

// IsZero reports an unset value (a row written before hlc existed, or a
// hand-made live file).
func (h HLC) IsZero() bool { return h.Wall.IsZero() && h.Counter == 0 && h.Actor == "" }

// ParseHLC parses String's form. "" is the zero value, not an error: a live
// file without an hlc sorts before every stamped edit.
func ParseHLC(s string) (HLC, error) {
	if s == "" {
		return HLC{}, nil
	}
	parts := strings.SplitN(s, hlcSep, hlcParts)
	if len(parts) != hlcParts {
		return HLC{}, fmt.Errorf("live: hlc %q: want wall%scounter%sactor", s, hlcSep, hlcSep)
	}
	wall, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return HLC{}, fmt.Errorf("live: hlc %q: %w", s, err)
	}
	n, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return HLC{}, fmt.Errorf("live: hlc %q counter: %w", s, err)
	}
	return HLC{Wall: wall.UTC().Truncate(hlcWallPrecision), Counter: uint32(n), Actor: parts[2]}, nil
}

// Compare orders by wall time, then counter, then actor (a total order, so
// two machines always agree which edit is newer). Returns -1, 0 or 1.
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
	return strings.Compare(h.Actor, o.Actor)
}

// NextHLC issues the stamp for a write that saw prev (the version being
// edited; zero for a new row): the local clock when it is ahead of prev,
// otherwise prev's wall with the counter raised. The result is always
// strictly greater than prev, whatever now says.
func NextHLC(prev HLC, now time.Time, actor string) HLC {
	wall := now.UTC().Truncate(hlcWallPrecision)
	if wall.After(prev.Wall) {
		return HLC{Wall: wall, Actor: actor}
	}
	next := HLC{Wall: prev.Wall, Counter: prev.Counter + 1, Actor: actor}
	if next.Counter == 0 { // wrapped: move the wall on instead
		next = HLC{Wall: prev.Wall.Add(hlcWallPrecision), Actor: actor}
	}
	return next
}

// MaxHLC returns the larger of two values.
func MaxHLC(a, b HLC) HLC {
	if a.Compare(b) >= 0 {
		return a
	}
	return b
}
