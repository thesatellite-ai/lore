//go:build teame2e

package teame2e

// §7.4 / task done: a human finishing a task whose claim expired is an
// override (claim dropped, epoch raised, done written in one signals write).
// The done mark now goes through coord.OverrideChecked, so a refused mark
// fails the command instead of being dropped; this runs the success path
// with the real binary and shows the old holder is fenced out.

import (
	"encoding/json"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestF07_HumanDoneOnExpiredClaim(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "done"))
	id := cr.task(t, "expired work")
	_, ac := cr.agent(t, "backend-1")
	ac.MustLore("task", "claim", id)

	// Alice sees the claim, then nothing renews it past ttl + grace: it is
	// expired, and finishing it is an override.
	cr.alice.SetNow(t0.Add(time.Minute))
	cr.toolsC.MustLore("task", "list")
	cr.alice.SetNow(t0.Add(leaseTTL + leaseGrace + time.Minute))
	r := cr.toolsC.MustLore("task", "done", id, "--pr", "41")
	t.Logf("human done on an expired claim:\n%s", r.Out())
	if _, ok := cr.claim(t, id); ok {
		t.Fatal("finishing an expired claim must drop it")
	}
	row, ok := readRow(t, cr.tools, refLive, liveRowPath(rootFolder, tblTasks, id))
	if !ok {
		row = mustRow(t, cr.tools, refMain, mainRowPath(rootFolder, tblTasks, id))
	}
	var pr int
	if raw, ok := row.Raw["done_pr"]; !ok || json.Unmarshal(raw, &pr) != nil || row.Status != statusDone || pr != 41 {
		t.Fatalf("row after the override done: status %q done_pr %s", row.Status, row.Raw["done_pr"])
	}
	z := ac.Lore("task", "done", id).Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	t.Logf("old holder's done afterwards:\n%s", z.Out())
}
