//go:build teame2e

package main

// lore-docs BUG-D3 / BUG-D4: what a replacement agent reads (§7.5 step 5)
// from any linked client repo of the group, without naming the source.

import (
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// BUG-D3: `task show <store task id>` without --from walks every loaded
// source (F1), like `task done` and `mission status` do, and shows what a
// replacement needs: the claim, the notes, the after list and the gate.
func TestF01_TaskShowWalksEverySource(t *testing.T) {
	g := newGlobexGroup(t, th.Cmd("task", "note"), th.Cmd("gate", "request"))
	g.beAgent.SetNow(t0)
	g.alice.SetNow(t0.Add(time.Minute))
	g.beClone.MustLore("task", "next", "--mission", g.mission, "--repo", "backend").Says(t, g.t30)
	g.beClone.MustLore("task", "note", g.t30, "mutation inviteUser(email) returns token")
	g.aliceFE.MustLore("gate", "request", g.t31)

	// A linked client repo that is not the task's repo, without --from.
	g.aliceFE.MustLore("task", "show", g.t30).
		Says(t, "invite mutation", "in_progress", "backend-1", "mutation inviteUser(email) returns token", g.mission)
	g.aliceFE.MustLore("task", "show", g.t31).Says(t, "landing route", "after", g.t30, "approval requested")
	// The repo has no rows of its own: the list shows the store rows and no
	// "(no tasks)" line above them.
	g.aliceFE.MustLore("task", "list").Says(t, g.t30, g.t31).Never(t, "(no tasks)")
	// --from gives the same full view.
	g.aliceFE.MustLore("task", "show", g.t30, "--from", orgNorthwind+"/"+folderGlobex).
		Says(t, "backend-1", "mutation inviteUser(email) returns token")
	// A done store task is still found.
	g.beClone.MustLore("task", "done", g.t30, "--pr", "412")
	g.aliceFE.MustLore("task", "show", g.t30).Says(t, "invite mutation", "done")
	// An unknown id is still E_NOT_FOUND; 0.1.18 exits 27 for it (DOCS O4).
	g.aliceFE.Lore("task", "show", "tsk_00000000000000000000000000").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
}

// BUG-D4: `mission status` names every agent by its label, also agents
// started in their own clones that never wrote into the store project.
func TestWorkedExample75_StatusNamesAgentsByLabel(t *testing.T) {
	g := newGlobexGroup(t, th.Cmd("mission", "status"), th.Flag("timebox", "task", "add"))
	to := orgNorthwind + "/" + folderGlobex
	id := mustID(t, g.aliceFE.MustLore("task", "add", "invite form", "--to", to, "--mission", g.mission, "--repo", "frontend", "--timebox", "1h"), "tsk")
	g.feAgent.SetNow(t0)
	g.feClone.MustLore("task", "claim", id)
	fe2 := startAgent(t, g.alice, g.aliceFE, "frontend-2", "alice")
	fe2.SetNow(t0.Add(leaseTTL + leaseGrace + 6*time.Minute))
	fe2c := fe2.Clone(g.frontend, "frontend")
	// The first look starts the two-part expiry watch; the second, a
	// renew interval later, may reclaim.
	fe2c.Lore("task", "next", "--mission", g.mission, "--repo", "frontend").Fails(t, th.ExitNoWork)
	fe2.Advance(leaseRenew + time.Minute)
	fe2c.MustLore("task", "next", "--mission", g.mission, "--repo", "frontend").Says(t, id, "reclaim")
	g.alice.SetNow(t0.Add(leaseTTL + leaseGrace + 20*time.Minute))
	g.aliceBE.MustLore("mission", "status", g.mission).Says(t, "invite form", "frontend-1", "frontend-2").Never(t, "act_")
}
