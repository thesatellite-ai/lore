//go:build teame2e

package main

// Fixed rule 6 (fail closed) on the task write path of a linked client
// repo: when lore cannot reach the store that coordinates a client task,
// `task done` (with a lesson), `task cancel` and `finding add` stop with an error and
// write nothing into the client repo — no done row, no lesson.

import (
	"os"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// breakStoreAccess removes alice's store clones and cache and cuts her
// network: lore can no longer read the store or its link index.
func (a *agency) breakStoreAccess(t *testing.T) {
	t.Helper()
	for _, dir := range []string{"clones", "cache"} {
		if err := os.RemoveAll(a.alice.LorePath(dir)); err != nil {
			t.Fatal(err)
		}
	}
	a.alice.Offline()
}

// TestF16_TaskDoneFailsClosedWithoutTheStore: a client task finished while
// the store is unreadable is refused; once the network is back and alice
// syncs, the client repo still holds neither a done row nor the lesson.
func TestF16_TaskDoneFailsClosedWithoutTheStore(t *testing.T) {
	a := newAgency(t, th.Flag("lesson", "task", "done"), th.Cmd("finding", "add"))
	id := mustID(t, a.acmeApp.MustLore("task", "add", "fix rounding", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	a.aliceApp.MustLore("sync")

	a.breakStoreAccess(t)
	a.aliceApp.Lore("task", "done", id, "--lesson", internalNote).Refused(t).Never(t, "✓ "+id+" done")
	// A finding has no task to fail first: its routing must refuse too.
	a.aliceApp.Lore("finding", "add", "--mission", "mis_unreadable", "finding: "+internalNote).Refused(t)
	a.aliceApp.Lore("task", "cancel", id).Refused(t)

	a.alice.Online()
	a.aliceApp.Lore("sync")
	if row := a.clientTaskRow(t, id); row.Status == statusDone || row.Status == "cancelled" {
		t.Fatalf("a refused done/cancel must not reach the client row: status %q", row.Status)
	}
	if f := a.clientSees(t, "send the estimate before the call"); len(f) != 0 {
		t.Fatalf("the lesson of a refused done reached the client repo: %v", f)
	}
}

// TEAM-030: a team cannot be assigned on a task written into a linked
// client repo (team names are internal). The team lives on the org roster,
// not in the client's lore.db, so the refusal must still recognise the
// handle as a team — not answer "no person or team" — and write nothing.
func TestF14_TeamAssignInClientRepoRefused(t *testing.T) {
	a := newAgency(t, th.Cmd("team", "add"), th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"))
	a.aliceApp.MustLore("team", "add", "@backend", "--members", "@alice")
	const title = "rounding fix for the team"
	a.aliceApp.Lore("task", "add", title, "--to", "repo", "--tasklist", a.tl, "--assign", "@backend").
		Refused(t, "E_INVALID_INPUT", "TEAM-030")
	a.aliceApp.Lore("sync")
	if f := a.clientSees(t, title, "backend"); len(f) != 0 {
		t.Fatalf("a refused team assignment must write nothing into the client repo: %v", f)
	}
	// A person handle still works in the client repo.
	a.aliceApp.MustLore("task", "add", "rounding fix for alice", "--to", "repo", "--tasklist", a.tl, "--assign", "@alice")
}
