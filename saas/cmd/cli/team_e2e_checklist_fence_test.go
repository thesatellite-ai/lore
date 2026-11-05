//go:build teame2e

package main

// TASK 7.4 / PASS3-A: a checklist write by a claim holder is fenced like
// `task note` (E_LOST_CLAIM once the claim is gone); a write by someone
// who never held the task is not fenced (a reviewer ticks "reviewed").

import (
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

func TestF07_ChecklistHolderFence(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "revoke"), th.Cmd("task", "checklist", "tick"))
	id := cr.task(t, "checklist work")
	_, ac := cr.agent(t, "backend-1")
	ac.MustLore("task", "claim", id)
	// the holder's own checklist writes pass the fence while it holds.
	ac.MustLore("task", "checklist", "add", id, "stream rows", "--item", "reviewed")
	ac.MustLore("task", "checklist", "tick", id, "1")

	cr.toolsC.MustLore("task", "revoke", id)
	z := ac.Lore("task", "checklist", "untick", id, "1").Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	t.Logf("old holder's checklist write after the revoke:\n%s", z.Out())

	// a reviewer who never held the task ticks unfenced.
	cr.toolsC.MustLore("task", "checklist", "tick", id, "2")
	cr.toolsC.MustLore("task", "checklist", "list", id).Says(t, "2/2 done")
}
