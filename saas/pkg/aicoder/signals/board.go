package signals

import (
	"sort"
	"time"
)

// Board is a parsed signals tree: tree path → signal. It is the state one
// signals commit holds.
type Board map[string]Signal

// ParseBoard decodes every signal file of a signals tree. Files that are not
// signal paths or do not decode are returned in bad (sorted): readers ignore
// them and the next writer drops them, so one corrupt file never blocks
// coordination.
func ParseBoard(tree map[string][]byte) (Board, []string) {
	b := Board{}
	var bad []string
	for p, body := range tree {
		k, ok := ParsePath(p)
		if !ok {
			bad = append(bad, p)
			continue
		}
		s, err := Decode(body)
		if err != nil || s.Kind != k.Kind {
			bad = append(bad, p)
			continue
		}
		b[p] = s
	}
	sort.Strings(bad)
	return b, bad
}

// View is one reader's view of a board at one instant: what is on the ref,
// what this clone has observed, and its clock and lease settings. Every
// decision takes a View, so it is a pure function of its inputs.
type View struct {
	Board Board
	// Seen must already include Board (Observations.Observe).
	Seen  Observations
	Now   time.Time
	Lease Lease
}

// Active returns the signal at p unless it is absent or expired for this
// reader.
func (v View) Active(p string) (Signal, bool) {
	s, ok := v.Board[p]
	if !ok || v.Seen.Expired(p, s, v.Now, v.Lease) {
		return Signal{}, false
	}
	return s, true
}

// Expired returns the paths this reader treats as expired, sorted. A writer
// drops them (fixed rule 10).
func (v View) Expired() []string {
	var out []string
	for _, p := range sortedPaths(v.Board) {
		if v.Seen.Expired(p, v.Board[p], v.Now, v.Lease) {
			out = append(out, p)
		}
	}
	return out
}

// ActiveClaimsOf returns the active claims held by actor in any folder,
// sorted by path. Mission claims are included; callers filter by kind of
// subject when counting the WIP limit.
func (v View) ActiveClaimsOf(actor string) []Held {
	var out []Held
	for _, p := range sortedPaths(v.Board) {
		s, ok := v.Active(p)
		if ok && s.Kind == KindClaim && s.Holder == actor {
			out = append(out, Held{Path: p, Signal: s})
		}
	}
	return out
}

// Held is one signal with its path.
type Held struct {
	Path   string
	Signal Signal
}
