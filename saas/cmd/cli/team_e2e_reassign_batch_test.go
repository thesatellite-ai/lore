//go:build teame2e

package main

import (
	"regexp"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// batchIDRe finds the bat_ id a bulk action prints.
var batchIDRe = regexp.MustCompile(`bat_[0-9a-f]{32}`)

// TASK 18.1 / TSQ6 (P3-COORD-2): `task reassign --from @handle` is one
// bulk change: every open task of that person is reassigned under one bat_
// id, the output names it, and `lore batch show` lists the tasks.
func TestRecipe09_ReassignFromIsOneBatch(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "reassign"), th.Cmd("batch", "show"), th.Flag("assign", "task", "add"))
	a := cr.task(t, "left behind one", "--assign", "@dev1")
	b := cr.task(t, "left behind two", "--assign", "@dev1")
	r := cr.toolsC.MustLore("task", "reassign", "--from", "@dev1", "--assign", "@alice").
		Says(t, a+" reassigned to @alice", b+" reassigned to @alice", "2 tasks reassigned to @alice (batch bat_")
	batch := batchIDRe.FindString(r.Out())
	if batch == "" {
		t.Fatalf("no batch id in %q", r.Out())
	}
	cr.toolsC.MustLore("batch", "show", batch, "--all").Says(t, a, b)
	cr.toolsC.MustLore("task", "reassign", "--from", "@dev1", "--assign", "@alice").Says(t, "no open tasks assigned to @dev1")
}
