//go:build teame2e

package teame2e

// Two clones, one task (A-TASK-16, FB-TASK-1): an agent claims a task in
// its own checkout, the person deletes that task from another checkout.
// The agent's inbox says so, the trashed task's claim no longer blocks a
// new claim (max_claims_per_agent 1), `task release` of the trashed task
// drops the claim, and a restore does not hand the claim back.

import (
	"testing"
)

func TestF07_TrashedClaimReleasesAcrossClones(t *testing.T) {
	cr := newCrew(t)
	doomed := cr.task(t, "doomed task")
	next := cr.task(t, "next task")
	_, ac := cr.agent(t, "agent-b")

	ac.MustLore("task", "claim", doomed).Says(t, "claimed "+doomed)
	cr.toolsC.MustLore("task", "delete", doomed, "--yes").Says(t, "moved to the trash")

	// The agent learns of it in its inbox (TASK 20 "deleted").
	ac.MustLore("inbox").Says(t, "deleted", doomed, "lore task release "+doomed)

	// The trashed task does not count toward the WIP limit of 1.
	ac.MustLore("task", "claim", next).Says(t, "claimed "+next)

	// Release finds the claim of a trashed task and drops it.
	ac.MustLore("task", "release", doomed).Says(t, "released "+doomed)
	if _, held := cr.claim(t, doomed); held {
		t.Fatalf("release of a trashed task must drop its claim from the signals ref")
	}

	// A restore does not hand the claim back.
	cr.toolsC.MustLore("trash", "restore", doomed)
	if _, held := cr.claim(t, doomed); held {
		t.Fatalf("a restored task must not get its old claim back")
	}
}
