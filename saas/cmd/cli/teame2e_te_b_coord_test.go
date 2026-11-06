//go:build teame2e

package main

// TASK_SYSTEM_DESIGN 6.9 with a remote: a claim of a parent with open
// sub-tasks is refused E_HAS_OPEN_CHILDREN (naming the ready children)
// unless --coordinate, which takes a coordinator lease on the signals ref
// of the remote that holds the task; the lease does not stop a second agent
// on its own clone from claiming a child, and a second coordinator is
// refused. Run twice: an own repo's task (the repo's refs/lore/signals) and
// an org store task worked from a linked client repo (the store's
// refs/lore/signals/main, folder acme/).

import (
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// teBCoordCase is one place a parent task can live.
type teBCoordCase struct {
	parent string
	kids   []string
	w1, w2 *th.Actor
	c1, c2 *th.Clone
	// claimOf reads the claim on a task from the remote's signals ref.
	claimOf func(id string) (signal, bool)
	// person is the clone of the person who made the tasks.
	person *th.Clone
}

// teBCoordOwnRepo builds the own-repo case: alice splits a task of
// northwind/tools, two agents each have their own checkout.
func teBCoordOwnRepo(t *testing.T) teBCoordCase {
	t.Helper()
	cr := newCrew(t, th.Cmd("task", "split"), th.Flag("coordinate", "task", "claim"))
	p := cr.task(t, "invoices export")
	_, kids := splitIDs(t, cr.toolsC, p, "export query", "download button")
	w1, c1 := cr.agent(t, "w-1")
	w2, c2 := cr.agent(t, "w-2")
	return teBCoordCase{parent: p, kids: kids, w1: w1, w2: w2, c1: c1, c2: c2, person: cr.toolsC,
		claimOf: func(id string) (signal, bool) { return cr.claim(t, id) }}
}

// teBCoordStore builds the store case: alice adds a task to the org store
// from the linked client repo acme/app (acme/ project) and splits it; two
// agents each have their own checkout of the client repo.
func teBCoordStore(t *testing.T) teBCoordCase {
	t.Helper()
	a := newAgency(t, append(append([]th.Cap{}, crewCaps...), th.Cmd("task", "split"), th.Flag("coordinate", "task", "claim"), th.Flag("to", "task", "add"))...)
	p := mustID(t, a.aliceApp.MustLore("task", "add", "invoices export", "--to", orgNorthwind), "tsk")
	if _, ok := a.storeLiveRow(t, folderAcme, tblTasks, p); !ok {
		if _, ok := a.storeMainRow(t, folderAcme, tblTasks, p); !ok {
			t.Fatalf("premise: the parent is an org store row of acme/ (live: %v)", a.store.Files(storeLiveRef))
		}
	}
	_, kids := splitIDs(t, a.aliceApp, p, "export query", "download button")
	w1 := startAgent(t, a.alice, a.aliceApp, "w-1", "alice")
	w2 := startAgent(t, a.alice, a.aliceApp, "w-2", "alice")
	c1, c2 := w1.Clone(a.app, "app"), w2.Clone(a.app, "app")
	return teBCoordCase{parent: p, kids: kids, w1: w1, w2: w2, c1: c1, c2: c2, person: a.aliceApp,
		claimOf: func(id string) (signal, bool) {
			return readSignal(t, a.store, storeSignals(), claimPath(folderAcme, id))
		}}
}

func TestTeB_CoordinatorLeaseWithARemote(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		build func(*testing.T) teBCoordCase
	}{
		{"own repo", teBCoordOwnRepo},
		{"org store from a client repo", teBCoordStore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := tc.build(t)
			// A plain claim of the parent is refused and names the ready children.
			k.c1.Lore("task", "claim", k.parent).Fails(t, th.ExitRefused, "E_HAS_OPEN_CHILDREN", k.kids[0], k.kids[1])
			if s, ok := k.claimOf(k.parent); ok {
				t.Fatalf("a refused claim leaves no lease on the remote: %+v", s)
			}
			// --coordinate takes a coordinator lease on the remote's signals ref.
			k.c1.MustLore("task", "claim", k.parent, "--coordinate")
			if s, ok := k.claimOf(k.parent); !ok || s.Session != k.w1.Env(th.EnvLoreSession) {
				t.Fatalf("the coordinator lease is on the remote's signals ref, held by w-1: %+v found=%v", s, ok)
			}
			// A second agent, on its own clone, claims a child although w-1
			// coordinates the parent.
			k.c2.MustLore("task", "claim", k.kids[0]).Says(t, "claimed")
			if s, ok := k.claimOf(k.kids[0]); !ok || s.Session != k.w2.Env(th.EnvLoreSession) {
				t.Fatalf("w-2 holds the child on the remote: %+v found=%v", s, ok)
			}
			// A second coordinator is told who coordinates.
			k.c2.Lore("task", "claim", k.parent, "--coordinate").Refused(t, "w-1")
			if s, _ := k.claimOf(k.parent); s.Session != k.w1.Env(th.EnvLoreSession) {
				t.Fatalf("the refused second coordinator leaves w-1's lease alone: %+v", s)
			}
			// The coordinator cannot close the parent while a child is open.
			k.c1.Lore("task", "done", k.parent, "--no-lesson").Refused(t, "E_OPEN_CHILDREN")
			// The children close; the parent is a leaf again and closes.
			k.c2.MustLore("task", "done", k.kids[0], "--no-lesson")
			k.c1.MustLore("task", "claim", k.kids[1])
			k.c1.MustLore("task", "done", k.kids[1], "--no-lesson")
			k.c1.MustLore("task", "done", k.parent, "--no-lesson")
			if s := subtaskStatus(t, k.person, k.parent); s != statusDone {
				t.Fatalf("the person's clone sees the parent done: %s", s)
			}
		})
	}
}
