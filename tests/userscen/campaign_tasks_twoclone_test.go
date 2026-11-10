//go:build userscen

package userscen

// Two people, each in their own clone of one bare remote, working one
// task tree the way a team does: a split in one clone reaches the other at
// once (live), a sub-task claimed in one clone is E_CONTENDED in the other,
// a task moved to another tasklist or parent in one clone shows moved in
// the other, and a parent cycle made by two concurrent moves (A under B in
// one clone, B under A in the other) is found by `task tree check` in both
// clones and repaired once by --fix. With live off (LORE_LIVE=0) a task
// waits for a commit and push; with the remote unreachable, a live add
// still works locally (pushed later) while a claim is E_OFFLINE with a
// hint and writes nothing; and two processes adding tasks at once in one
// clone both land.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignTasks_TwoCloneSplitClaimMove: split, claim and move across
// two clones.
func TestCampaignTasks_TwoCloneSplitClaimMove(t *testing.T) {
	tm := newTasksTeam(t)
	parent := tasksAdd(t, tm.ca, "Invite flow", "--tasklist", tm.tasklist)
	tm.ca.MustLore("task", "split", parent, "API", "Email", "UI")
	var kids []string
	for _, id := range tasksList(t, tm.cb, "--all") {
		if tasksShow(t, tm.cb, id).ParentID == parent {
			kids = append(kids, id)
		}
	}
	if len(kids) != 3 {
		t.Fatalf("bob's clone sees %d sub-tasks of alice's split, want 3", len(kids))
	}
	// A claim in one clone is contended in the other; the parent of open
	// sub-tasks is never handed out by next.
	tm.cb.MustLore("task", "claim", kids[0]).Says(t, "claimed")
	tm.ca.Lore("task", "claim", kids[0]).Fails(t, th.ExitContended, "E_CONTENDED")
	tm.ca.MustLore("task", "next").Never(t, parent, kids[0])

	// Move: alice moves a sub-task to another list and under another
	// parent; bob sees both moves.
	other := tasksID(t, tm.ca.MustLore("tasklist", "add", "--title", "Later", "--body", "parked", "--json"))
	tm.ca.MustLore("task", "edit", kids[2], "--tasklist", other)
	newParent := tasksAdd(t, tm.ca, "Polish", "--tasklist", tm.tasklist)
	tm.ca.MustLore("task", "edit", kids[1], "--parent", newParent)
	if got := tasksShow(t, tm.cb, kids[2]); got.TaskListID != other {
		t.Fatalf("bob sees the moved task in tasklist %s, want %s", got.TaskListID, other)
	}
	if got := tasksShow(t, tm.cb, kids[1]); got.ParentID != newParent {
		t.Fatalf("bob sees the moved task under %s, want %s", got.ParentID, newParent)
	}
	for _, c := range []*th.Clone{tm.ca, tm.cb} {
		r := c.MustLore("task", "tree", "check", "--json")
		campTasksCheckShape(t, "task.tree.check", r.Stdout)
	}
	// Moving a task under its own descendant is refused with a hint.
	e := campTasksErr(t, tm.cb.Lore("task", "edit", parent, "--parent", kids[0], "--json"))
	if e.Error.Hint == "" {
		t.Errorf("a move into its own subtree: refused without a hint: %+v", e.Error)
	}
}

// TestCampaignTasks_TwoCloneCycleRepair: two moves that are each fine
// alone make a cycle once both are in (A under B in alice's clone while bob,
// offline, puts B under A). tree check reports it (E_PARENT_CYCLE) in both
// clones; --fix repairs it and both clones then check clean.
func TestCampaignTasks_TwoCloneCycleRepair(t *testing.T) {
	tm := newTasksTeam(t)
	a := tasksAdd(t, tm.ca, "task A", "--tasklist", tm.tasklist)
	b := tasksAdd(t, tm.ca, "task B", "--tasklist", tm.tasklist)
	tasksShow(t, tm.cb, b) // bob has both
	tm.bob.Offline()
	tm.ca.MustLore("task", "edit", a, "--parent", b)
	tm.cb.MustLore("task", "edit", b, "--parent", a) // bob still sees no cycle: his write waits
	tm.bob.Online()
	tm.cb.Lore("sync", "run")
	tm.ca.Lore("sync", "run")
	for _, c := range []*th.Clone{tm.ca, tm.cb} {
		r := c.MustLore("task", "tree", "check", "--json")
		if !strings.Contains(r.Stdout, `"E_PARENT_CYCLE"`) || !strings.Contains(r.Stdout, a) || !strings.Contains(r.Stdout, b) {
			t.Fatalf("%s's tree check does not report the cycle both moves made: %s", c.A.Name, r)
		}
		campTasksCheckShape(t, "task.tree.check", r.Stdout)
	}
	tm.ca.Lore("task", "tree", "check", "--fix").OK(t)
	for _, c := range []*th.Clone{tm.ca, tm.cb} {
		pa, pb := tasksShow(t, c, a).ParentID, tasksShow(t, c, b).ParentID
		if pa == b && pb == a {
			t.Fatalf("after --fix the cycle is still there in %s's clone", c.A.Name)
		}
		c.MustLore("task", "tree", "check").Never(t, "cycle")
	}
	tm.ca.MustLore("task", "tree", "check", "--fix").Never(t, "cycle") // a second --fix has nothing to do
}

// TestCampaignTasks_LiveOffAndOffline: live off waits for a commit; an
// unreachable remote keeps a live add local and refuses a claim cleanly.
func TestCampaignTasks_LiveOffAndOffline(t *testing.T) {
	tm := newTasksTeam(t)
	off := tasksAdd(t, tm.ca.With("LORE_LIVE=0"), "Update the privacy page", "--tasklist", tm.tasklist)
	if tasksHas(tasksList(t, tm.cb, "--all"), off) {
		t.Fatal("a LORE_LIVE=0 task reached bob before any commit")
	}
	if _, ok := tm.ca.ReadFile(".lore/data/tasks/" + off + ".json"); !ok {
		t.Fatal("a LORE_LIVE=0 task is not in .lore/data")
	}
	tm.ca.CommitAll("privacy task")
	tm.ca.AdminPush("origin", "main")
	tm.cb.Git("pull", "-q", "origin", "main")
	if !tasksHas(tasksList(t, tm.cb, "--all"), off) {
		t.Fatal("after commit, push and pull bob does not have the task")
	}

	tm.alice.Offline()
	local := tasksAdd(t, tm.ca, "Renew the SSL certificate", "--tasklist", tm.tasklist)
	if !tasksHas(tasksList(t, tm.ca), local) {
		t.Fatal("an offline live add is not in alice's own list")
	}
	before := campTasksTreeHash(t, tm.ca.Dir)
	e := campTasksErr(t, tm.ca.Lore("task", "claim", local, "--json"))
	if e.Error.Code != "E_OFFLINE" || e.Error.Hint == "" {
		t.Fatalf("an offline claim: want E_OFFLINE with a hint: %+v", e.Error)
	}
	if campTasksTreeHash(t, tm.ca.Dir) != before {
		t.Fatal("a refused offline claim changed the clone")
	}
	// A typo is a typo even offline: the id is checked before the network.
	if e := campTasksErr(t, tm.ca.Lore("task", "claim", "nope", "--json")); e.Error.Code != "E_INVALID_ID" {
		t.Errorf("an offline claim of a typo: want E_INVALID_ID, got %+v", e.Error)
	}
	tm.alice.Online()
	tm.ca.MustLore("sync", "run")
	if !tasksHas(tasksList(t, tm.cb, "--all"), local) {
		t.Fatal("back online, the offline add did not reach bob")
	}
	tm.ca.MustLore("task", "claim", local).Says(t, "claimed")
}

// TestCampaignTasks_ConcurrentAdds: two processes adding tasks in one
// clone at the same moment both land, with distinct ids, and a claim race
// between two agents has exactly one winner.
func TestCampaignTasks_ConcurrentAdds(t *testing.T) {
	ta := newTasksAgents(t)
	res := ta.w.Simultaneously(
		ta.ca.Prepare("task", "add", "racer one", "--tasklist", ta.tasklist, "--json"),
		ta.ca.Prepare("task", "add", "racer two", "--tasklist", ta.tasklist, "--json"),
		ta.ca.Prepare("task", "add", "racer three", "--tasklist", ta.tasklist, "--json"),
	)
	seen := map[string]bool{}
	for _, r := range res {
		seen[tasksID(t, r)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("three concurrent adds gave %d distinct ids", len(seen))
	}
	for id := range seen {
		if !tasksHas(tasksList(t, ta.cb, "--all"), id) {
			t.Errorf("concurrent add %s did not reach bob", id)
		}
	}
	id := tasksAdd(t, ta.ca, "contested", "--tasklist", ta.tasklist)
	ta.c1.MustLore("task", "list")
	ta.c2.MustLore("task", "list")
	race := ta.w.Simultaneously(ta.c1.Prepare("task", "claim", id), ta.c2.Prepare("task", "claim", id))
	wins := 0
	for _, r := range race {
		switch {
		case r.Code == 0:
			wins++
		case r.Code != th.ExitContended:
			t.Errorf("a losing claim must be E_CONTENDED: %s", r)
		}
	}
	if wins != 1 {
		t.Fatalf("a claim race had %d winners, want exactly 1", wins)
	}
}
