//go:build userscen

package userscen

// Claims and leases between two people's agents, each in their own clone
// of one remote: claim, a second claim of a held task (E_CONTENDED, exit
// 11, nothing changes), release and heartbeat by someone who does not
// hold it (refused), release then claim by the other, a lease that
// expires and is taken over, a person's revoke, the same claim twice by
// its holder, `task next` picking a ready leaf and saying E_NO_WORK (exit
// 10) when nothing is ready, and claims of closed tasks.

import (
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksLeaseGone is past a lease's TTL and grace (20m + 2m, LORE_TEAM_SPEC
// §7.4) with no heartbeat.
const tasksLeaseGone = 23 * time.Minute

// tasksAgents is alice's and bob's agents, each in its own clone.
type tasksAgents struct {
	*tasksTeam
	ag1, ag2 *th.Actor
	c1, c2   *th.Clone
}

// newTasksAgents starts one agent per person; both clones see the remote.
func newTasksAgents(t *testing.T) *tasksAgents {
	t.Helper()
	tm := newTasksTeam(t)
	ta := &tasksAgents{tasksTeam: tm}
	ta.ag1 = tm.alice.Agent("alice-bot")
	ta.ag2 = tm.bob.Agent("bob-bot")
	remote := tm.w.Remote(tasksSlug)
	ta.c1 = ta.ag1.Clone(remote, "app-agent")
	ta.c2 = ta.ag2.Clone(remote, "app-agent")
	ta.c1.MustLore("task", "list")
	ta.c2.MustLore("task", "list")
	return ta
}

// at sets every actor's clock.
func (ta *tasksAgents) at(when time.Time) {
	for _, a := range []*th.Actor{ta.alice, ta.bob, ta.ag1, ta.ag2} {
		a.SetNow(when)
	}
}

// A held task cannot be taken: the second agent's claim is E_CONTENDED,
// its release and heartbeat are refused, the holder's own repeat claim is
// fine; after the holder releases, the other agent claims it.
func TestTasksClaims_ContendedReleaseAndHandOver(t *testing.T) {
	ta := newTasksAgents(t)
	id := tasksAdd(t, ta.ca, "shared work", "--tasklist", ta.tasklist)
	ta.c1.MustLore("task", "claim", id).Says(t, "claimed")
	ta.c1.MustLore("task", "claim", id) // the holder again: still holds it

	ta.c2.Lore("task", "claim", id).Fails(t, th.ExitContended, "E_CONTENDED")
	ta.c2.Lore("task", "release", id).Refused(t)
	ta.c2.Lore("task", "heartbeat", id).Refused(t)
	ta.c2.Lore("task", "done", id, "--no-lesson").Refused(t)
	if st := tasksShow(t, ta.c1, id).Status; st == "done" {
		t.Fatal("a non-holder's done changes nothing")
	}

	ta.c1.MustLore("task", "heartbeat", id)
	ta.c1.MustLore("task", "release", id)
	ta.c1.Lore("task", "release", id).Refused(t) // nothing held any more
	ta.c2.MustLore("task", "claim", id).Says(t, "claimed")
	ta.c1.Lore("task", "claim", id).Fails(t, th.ExitContended, "E_CONTENDED")
	ta.c2.MustLore("task", "done", id, "--no-lesson")
	if got := tasksShow(t, ta.cb, id); got.Status != "done" || got.CloseReason != "completed" {
		t.Fatalf("the holder's done reaches bob's clone: %+v", got)
	}
	// A closed task cannot be claimed.
	ta.c1.Lore("task", "claim", id).Refused(t)
}

// A lease nobody renews expires; then another agent takes the task over
// (one more attempt), and the old holder's late heartbeat is fenced
// (E_LOST_CLAIM, exit 12).
func TestTasksClaims_ExpiredLeaseTakenOver(t *testing.T) {
	ta := newTasksAgents(t)
	id := tasksAdd(t, ta.ca, "long job", "--tasklist", ta.tasklist)
	ta.c1.MustLore("task", "claim", id)
	ta.at(tasksT0.Add(5 * time.Minute))
	ta.c2.Lore("task", "claim", id).Fails(t, th.ExitContended, "E_CONTENDED")
	ta.at(tasksT0.Add(tasksLeaseGone))
	ta.c2.MustLore("task", "claim", id).Says(t, "claimed")
	ta.c1.Lore("task", "heartbeat", id).Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	ta.c1.Lore("task", "done", id, "--no-lesson").Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	ta.c2.MustLore("task", "done", id, "--no-lesson")
}

// A person's revoke drops an agent's claim: the agent is fenced out
// (its heartbeat is E_LOST_CLAIM) and the other agent may claim it.
func TestTasksClaims_PersonRevokes(t *testing.T) {
	ta := newTasksAgents(t)
	id := tasksAdd(t, ta.ca, "stuck agent", "--tasklist", ta.tasklist)
	ta.c1.MustLore("task", "claim", id)
	ta.ca.MustLore("task", "revoke", id)
	ta.c1.Lore("task", "heartbeat", id).Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
	ta.c2.MustLore("task", "claim", id).Says(t, "claimed")
	// Revoking a task nobody holds is a no-op that says so.
	free := tasksAdd(t, ta.ca, "nobody holds me", "--tasklist", ta.tasklist)
	ta.ca.MustLore("task", "revoke", free).Says(t, "no one held it")
}

// task next claims the most urgent ready leaf: never a task waiting on an
// open dependency, never a parent with open sub-tasks (it becomes a leaf once they are done), never a task held
// by the other agent; with nothing left it is E_NO_WORK (exit 10). The
// --dry-run ranks and claims nothing.
func TestTasksClaims_NextPicksReadyLeaf(t *testing.T) {
	ta := newTasksAgents(t)
	low := tasksAdd(t, ta.ca, "low chore", "--tasklist", ta.tasklist, "--priority", "low")
	urgent := tasksAdd(t, ta.ca, "urgent fix", "--tasklist", ta.tasklist, "--priority", "urgent")
	waits := tasksAdd(t, ta.ca, "waits on urgent", "--tasklist", ta.tasklist, "--priority", "urgent", "--after", urgent)
	parent := tasksAdd(t, ta.ca, "parent", "--tasklist", ta.tasklist, "--priority", "urgent")
	leaf := tasksAdd(t, ta.ca, "leaf", "--parent", parent, "--priority", "high")

	ta.c1.MustLore("task", "next", "--dry-run").Says(t, urgent)
	ta.c2.MustLore("task", "next").Says(t, urgent)
	ta.c1.MustLore("task", "next").Says(t, leaf).Never(t, parent, waits, urgent)
	ta.c2.MustLore("task", "done", urgent, "--no-lesson")
	ta.c2.MustLore("task", "next").Says(t, waits)
	ta.c2.MustLore("task", "release", waits)
	ta.c1.MustLore("task", "done", leaf, "--no-lesson")
	ta.c1.MustLore("task", "next").Says(t, waits)
	// With its last sub-task done the parent is a leaf itself: next.
	ta.c2.MustLore("task", "next").Says(t, parent)
	ta.c2.MustLore("task", "done", parent, "--no-lesson")
	ta.c2.MustLore("task", "next").Says(t, low)
	// Resume first: an agent that holds a task is pointed back at it.
	ta.c2.MustLore("task", "next").Says(t, "you hold "+low, "resume")
	ta.c2.MustLore("task", "done", low, "--no-lesson")
	ta.c1.MustLore("task", "release", waits)
	ta.c1.MustLore("task", "next").Says(t, waits)
	ta.c2.Lore("task", "next").Fails(t, th.ExitNoWork, "E_NO_WORK")
}
