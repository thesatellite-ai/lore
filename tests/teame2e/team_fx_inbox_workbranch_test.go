//go:build teame2e

package teame2e

// devco:D-619: the owner's inbox names an expired claim's work branch from
// what git already has (the remote-tracking ref), not from the recorded
// work_branch alone: since D-598 a note no longer pushes the branch, so a
// recorded branch may exist only on the holder's machine.

import (
	"testing"
	"time"
)

func TestF07_InboxSaysWhetherTheWorkBranchIsPushed(t *testing.T) {
	const branch = "agent-b/search"
	cr := newCrew(t)
	id := cr.task(t, "search page")
	_, dc := cr.agent(t, "agent-b")

	// The holder claims on its work branch and never pushes it.
	dc.Git("checkout", "-q", "-b", branch)
	dc.MustLore("task", "claim", id).Says(t, "work branch "+branch)

	// The owner sees the lease once, then it expires past ttl + grace.
	cr.alice.SetNow(t0.Add(time.Minute))
	cr.toolsC.MustLore("task", "list")
	cr.alice.SetNow(t0.Add(leaseTTL + leaseGrace + time.Minute))
	cr.toolsC.MustLore("inbox").Says(t, id, "lease expired", "work branch "+branch+" (not pushed; push with: git push -u origin "+branch+")").
		Never(t, "work branch pushed")

	// Pushed and fetched here: the same item says where it is.
	dc.Git("push", "-q", "origin", branch)
	cr.toolsC.Git("fetch", "-q", "origin")
	cr.toolsC.MustLore("inbox").Says(t, id, "work branch "+branch+" (on origin)").Never(t, "not pushed")
}
