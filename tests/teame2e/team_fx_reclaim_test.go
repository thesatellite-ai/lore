//go:build teame2e

package teame2e

// §7.4 "Work branch" on real git with the real binary: a claim records the
// claimer's work branch, and a reclaim after the lease expired keeps it, so
// the replacement is sent to the dead agent's pushed work, not to the
// branch the replacement happens to have checked out.

import (
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestF07_ReclaimKeepsTheRecordedWorkBranch(t *testing.T) {
	const deadBranch, heirBranch = "agent-b/frontend", "agent-a/backend"
	cr := newCrew(t, th.Cmd("task", "next"))
	id := cr.task(t, "frontend route")
	_, dc := cr.agent(t, "agent-b")
	heir, hc := cr.agent(t, "agent-a")

	// The first holder claims on its work branch and pushes it.
	dc.Git("checkout", "-q", "-b", deadBranch)
	dc.MustLore("task", "claim", id).Says(t, "work branch "+deadBranch)
	dc.Git("push", "-q", "origin", deadBranch)
	if got := strField(cr.liveTask(t, id), "work_branch"); got != deadBranch {
		t.Fatalf("a first claim records the claimer's branch: work_branch=%q", got)
	}

	// The heir sees the lease once, then it expires past ttl + grace.
	hc.Git("checkout", "-q", "-b", heirBranch)
	heir.SetNow(t0.Add(time.Minute))
	hc.MustLore("task", "list")
	heir.SetNow(t0.Add(leaseTTL + leaseGrace + time.Minute))
	r := hc.MustLore("task", "next")
	r.Says(t, id, "reclaim", "work branch "+deadBranch)
	if strings.Contains(r.Out(), heirBranch) {
		t.Fatalf("the reclaim must not name the reclaimer's own branch:\n%s", r)
	}
	if got := strField(cr.liveTask(t, id), "work_branch"); got != deadBranch {
		t.Fatalf("a reclaim keeps the recorded work branch: work_branch=%q", got)
	}
	sig, _ := cr.claim(t, id)
	if sig.WorkBranch != deadBranch {
		t.Fatalf("the new claim names the recorded work branch: %+v", sig)
	}
}

// strField reads a string field of a row ("" when absent).
func strField(r rowDoc, field string) string {
	return strings.Trim(string(r.Raw[field]), `"`)
}
