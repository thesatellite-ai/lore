package sqlite

import (
	"testing"
	"time"
)

// TestCountSpans pins design 7.4: a worker's overlap counts once
// (counted_seconds), the owner subtotal folds agents in (TQ1), manual
// entries count in full.
func TestCountSpans(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.UTC) }
	spans := []spanRow{
		{taskID: "t", entryID: "a", actorID: "mira", ownerID: "mira", start: at(9, 0), end: at(11, 0)},
		{taskID: "t", entryID: "b", actorID: "mira", ownerID: "mira", start: at(10, 0), end: at(12, 0)},
		{taskID: "t", entryID: "c", actorID: "codex", ownerID: "mira", start: at(11, 0), end: at(13, 0)},
		{taskID: "t", entryID: "d", actorID: "mira", ownerID: "mira", start: at(10, 0), end: at(10, 30)},
		{taskID: "t", entryID: "m", actorID: "mira", ownerID: "mira", seconds: 1800, manual: true},
	}
	countSpans(spans)
	want := map[string][2]int64{ // entry → counted, owner_counted
		"a": {7200, 7200}, "b": {3600, 3600}, "c": {7200, 3600}, "d": {0, 0}, "m": {1800, 1800},
	}
	for _, s := range spans {
		if got := [2]int64{s.counted, s.ownerCounted}; got != want[s.entryID] {
			t.Errorf("%s: counted/owner = %v, want %v", s.entryID, got, want[s.entryID])
		}
	}
}
