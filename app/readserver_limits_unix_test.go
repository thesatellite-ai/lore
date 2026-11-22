//go:build linux || darwin

package app

import "testing"

// TestServerLimitsGrowWithKeptState (devco:D-887, issue #96 follow-up):
// the read server's live-heap exit limit and soft memory limit add what it
// keeps on purpose. A server of a 100,000-row store keeps ~250 MB of
// ask's answers; with the fixed 128 MB limit it exited after every call
// and its 192 MB soft limit sat below its live heap. Unbounded growth
// still ends it: past readServerMaxLive above the kept state.
func TestServerLimitsGrowWithKeptState(t *testing.T) {
	maxLive, memLimit := serverLimits(readServerMaxLive, 0)
	if maxLive != readServerMaxLive || memLimit != readServerMemLimit {
		t.Fatalf("serverLimits(readServerMaxLive, 0) = %d, %d; want the base limits %d, %d", maxLive, memLimit, uint64(readServerMaxLive), int64(readServerMemLimit))
	}
	const kept = 250 << 20
	live := uint64(kept + 64<<20) // the kept answers plus the server's own working heap
	maxLive, memLimit = serverLimits(readServerMaxLive, kept)
	if live > maxLive {
		t.Fatalf("a server keeping %d MB with a live heap of %d MB exits (limit %d MB): every call would run one-shot", kept>>20, live>>20, maxLive>>20)
	}
	if memLimit < int64(live) {
		t.Fatalf("soft memory limit %d MB is below the live heap %d MB: the server would collect without pause", memLimit>>20, live>>20)
	}
	if grown := kept + readServerMaxLive + 1; uint64(grown) <= maxLive {
		t.Fatalf("a live heap %d MB past the kept state still under the limit %d MB: growth nobody bounded would never end the server", (grown-kept)>>20, maxLive>>20)
	}
	if _, m := serverLimits(readServerMaxLive, -1); m != readServerMemLimit {
		t.Fatalf("a negative kept count changed the limits: %d", m)
	}
}

// TestOverLiveLimitCollectsBeforeExiting (devco:D-887): the live heap
// the server reads is as of the last collection, which may have run
// mid-call while the answer's temporary copies were reachable; a release
// build then exited after every ask on a big store. A reading over the
// limit is checked again after one full collection; a reading under it
// costs no collection.
func TestOverLiveLimitCollectsBeforeExiting(t *testing.T) {
	const limit = 100
	cases := []struct {
		name          string
		before, after uint64
		wantOver      bool
		wantCollected bool
	}{
		{"under: no collection", 80, 80, false, false},
		{"stale mid-call reading: collected, stays", 180, 90, false, true},
		{"really over: collected, exits", 180, 150, true, true},
	}
	for _, c := range cases {
		collected := false
		live := func() uint64 {
			if collected {
				return c.after
			}
			return c.before
		}
		got := overLiveLimit(limit, live, func() { collected = true })
		if got != c.wantOver || collected != c.wantCollected {
			t.Errorf("%s: overLiveLimit = %v (collected %v), want %v (collected %v)", c.name, got, collected, c.wantOver, c.wantCollected)
		}
	}
}
