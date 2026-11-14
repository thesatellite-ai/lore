package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
)

// TestLoneCompanionIsNotWaitingToLand (devco N-14): right after a lore
// sync, doctor said "1 row(s) waiting to land; the next live write or
// `lore sync` lands them" for the writer's actor row (a companion row):
// a sync never lands a companion row alone (F2), so no sync cleared it.
// Doctor and sync status count waiting rows from what the next landing
// pass takes; a companion row counts once a content row lands beside it.
func TestLoneCompanionIsNotWaitingToLand(t *testing.T) {
	t.Parallel()
	companion := landPlanRow{Key: live.RowKey{Table: "actors", ID: "act_a"},
		File:     live.File{Block: live.Block{Companion: true}},
		Decision: live.LandDecision{Action: live.LandFinal}}
	open := landPlanRow{Key: live.RowKey{Table: "tasks", ID: "tsk_open"},
		Decision: live.LandDecision{Action: live.LandStay}}
	done := landPlanRow{Key: live.RowKey{Table: "tasks", ID: "tsk_done"},
		Decision: live.LandDecision{Action: live.LandFinal}}
	if got := landsNext([]landPlanRow{companion, open}, live.RootFolder); len(got) != 0 {
		t.Fatalf("a lone companion row counts as waiting to land: %v", got)
	}
	got := landsNext([]landPlanRow{companion, open, done}, live.RootFolder)
	if !got[companion.Key] || !got[done.Key] || got[open.Key] || len(got) != 2 {
		t.Fatalf("with a finished row beside it, the companion and the finished row land, the open row stays: %v", got)
	}
}
