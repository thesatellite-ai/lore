package main

import (
	"testing"

	"saas/pkg/aicoder/live"
)

// A companion row never lands alone; with a content row, or written live on
// purpose (actor register, Q12), it lands.
func TestWithoutLoneSupporting(t *testing.T) {
	comp := live.LandItem{Key: live.RowKey{Table: "actors", ID: "act_1"}, File: live.File{Block: live.Block{Companion: true}}}
	task := live.LandItem{Key: live.RowKey{Table: "tasks", ID: "tsk_1"}}
	onPurpose := live.LandItem{Key: live.RowKey{Table: "actors", ID: "act_2"}}
	if got := withoutLoneSupporting([]live.LandItem{comp}); len(got) != 0 {
		t.Fatalf("a lone companion must not land: %v", got)
	}
	if got := withoutLoneSupporting([]live.LandItem{comp, task}); len(got) != 2 {
		t.Fatalf("a companion lands with its content row: %v", got)
	}
	if got := withoutLoneSupporting([]live.LandItem{onPurpose}); len(got) != 1 {
		t.Fatalf("an actor written live on purpose lands: %v", got)
	}
}
