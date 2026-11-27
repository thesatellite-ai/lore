package search

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/staleclaim"
)

// TestMarkContradictedHits is bug hunt H-289 (devco:D-1027): of two hits,
// the older says the retry queue is planned and the newer says it
// shipped: the older is marked stale naming the newer and ranks below a
// fresh hit with the same score; a hit without a claim (a card) and a
// planned hit nothing contradicts stay unmarked.
func TestMarkContradictedHits(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 3, n, 9, 0, 0, 0, time.UTC) }
	hits := []Hit{
		{ID: "mem_planned", Score: 1, Claim: &staleclaim.Doc{ID: "mem_planned", Body: "Planned: a retry queue for failed webhook deliveries.", At: day(1)}},
		{ID: "mem_shipped", Score: 1, Claim: &staleclaim.Doc{ID: "mem_shipped", Body: "The retry queue for webhook deliveries shipped in v0.4.", At: day(20)}},
		{ID: "mem_other", Score: 1, Claim: &staleclaim.Doc{ID: "mem_other", Body: "Planned: CSV export for the billing page.", At: day(2)}},
		{ID: "res_card", Entity: EntityResource, Score: 1},
	}
	MarkContradicted(hits)
	if hits[0].ContradictedBy != "mem_shipped" || hits[0].Stale != "stale: a newer row says it is done: mem_shipped (2026-03-20)" {
		t.Fatalf("planned hit: %+v", hits[0])
	}
	for _, h := range hits[1:] {
		if h.ContradictedBy != "" || h.Stale != "" {
			t.Fatalf("%s must stay unmarked: %+v", h.ID, h)
		}
	}
	if RankScore(hits[0]) >= RankScore(hits[1]) {
		t.Fatalf("the contradicted hit must rank below the fresh one: %v vs %v", RankScore(hits[0]), RankScore(hits[1]))
	}
}
