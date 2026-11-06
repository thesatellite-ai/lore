//go:build teame2e

package main

// TASK_SYSTEM_DESIGN 6.15 E2E (the rest of the two-clone task story) on
// real git with the real binary: dev1 deletes a parent with its sub-tasks,
// dev2's clone sees the whole subtree in the trash as one batch, a child
// alone is refused while its parent is in the trash, the batch comes back
// with the same ids and the same tree on both clones; after the parent is
// purged a child comes back only with a new home (--parent), and both
// clones agree on it.

import (
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// teABugLivePurgeList: on a repo with a live ref, `lore trash purge`
// writes the tombstone to the live ref but lists the purged id only in the
// working tree's untracked .lore/data/_purged.json (liveBackend.WritePurge
// and Purged use the inner backend's list, never the live tree's
// _live/purged.json). Right after the purge the purger's clone answers a
// restore E_PURGED; after `lore sync`, it and every teammate answer
// E_NOT_FOUND "was deleted (live, …)". Found by te-a; no owner yet.
var teABugLivePurgeList = th.Bug{ID: "BUG-LIVE-PURGE-LIST", Owner: "wire/host live purge, unassigned", Open: true}

// teATrashCaps is what the trash e2e needs beyond the repo team.
var teATrashCaps = []th.Cap{
	th.Cmd("trash", "list"), th.Cmd("trash", "restore"), th.Cmd("trash", "purge"),
	th.Cmd("task", "tree", "check"), th.Flag("parent", "trash", "restore"),
}

// teATrashed is `lore trash list --with-dependents --json` on c: row id ->
// batch id.
func teATrashed(t *testing.T, c *th.Clone) map[string]string {
	t.Helper()
	v := th.DecodeJSON[struct {
		Data []struct {
			ID    string `json:"id"`
			Batch string `json:"batch"`
		} `json:"data"`
	}](t, c.MustLore("trash", "list", "--with-dependents", "--json"))
	out := map[string]string{}
	for _, it := range v.Data {
		out[it.ID] = it.Batch
	}
	return out
}

// teAParent is the parent_id of task id on c; ok is false when the task is
// not live there.
func teAParent(t *testing.T, c *th.Clone, id string) (string, bool) {
	t.Helper()
	r := c.Lore("task", "show", id, "--json")
	if r.Code != th.ExitOK {
		return "", false
	}
	return th.DecodeJSON[struct {
		Data struct {
			Parent string `json:"parent_id"`
		} `json:"data"`
	}](t, r).Data.Parent, true
}

// teASameTree syncs both clones and asserts they show the same tree check
// and, for every id in want, the same parent (want[id]; "-" = not live).
func teASameTree(t *testing.T, step string, rt *repoTeam, want map[string]string) {
	t.Helper()
	rt.c1.MustLore("sync")
	rt.c2.MustLore("sync")
	rt.c1.MustLore("sync")
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		for id, wp := range want {
			p, ok := teAParent(t, c, id)
			got := p
			if !ok {
				got = "-"
			}
			if got != wp {
				t.Fatalf("%s: %s on %s has parent %q, want %q", step, id, c.Dir, got, wp)
			}
		}
	}
	r1 := rt.c1.Lore("task", "tree", "check")
	r2 := rt.c2.Lore("task", "tree", "check")
	if r1.Stdout != r2.Stdout || r1.Code != r2.Code {
		t.Fatalf("%s: both clones give the same tree check:\ndev1 %s\ndev2 %s", step, r1, r2)
	}
}

func TestTeA_TaskTrash_SubtreeRestoreTwoClones(t *testing.T) {
	rt := newRepoTeam(t, teATrashCaps...)
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	p := rt.addTask(t, rt.c1, "release 2.0")
	c1 := rt.addTask(t, rt.c1, "changelog", "--parent", p)
	c2 := rt.addTask(t, rt.c1, "tag", "--parent", p)
	adopter := rt.addTask(t, rt.c1, "release 2.1")
	teASameTree(t, "before the delete", rt, map[string]string{p: "", c1: p, c2: p, adopter: ""})

	// dev1 deletes the parent: the subtree goes to the trash as one batch,
	// which dev2's clone sees after its sync.
	rt.c1.MustLore("task", "delete", p, "--yes", "--reason", "replanned")
	teASameTree(t, "after the subtree delete", rt, map[string]string{p: "-", c1: "-", c2: "-", adopter: ""})
	trashed := teATrashed(t, rt.c2)
	batch := trashed[p]
	if batch == "" || trashed[c1] != batch || trashed[c2] != batch {
		t.Fatalf("the parent and both sub-tasks are in the trash as one batch on dev2's clone: %v", trashed)
	}

	// A child alone is refused on dev2's clone while its parent is in the
	// trash, and stays there.
	rt.c2.Lore("trash", "restore", c1).Refused(t, "E_PARENT_DELETED", p, batch)
	if teATrashed(t, rt.c2)[c1] != batch {
		t.Fatal("a refused restore leaves the child in the trash")
	}

	// dev2 restores the batch: same ids, same tree on both clones.
	rt.c2.MustLore("trash", "restore", batch)
	teASameTree(t, "after the batch restore", rt, map[string]string{p: "", c1: p, c2: p, adopter: ""})
	if left := teATrashed(t, rt.c1); len(left) != 0 {
		t.Fatalf("the trash is empty on dev1's clone after dev2's restore: %v", left)
	}

	// The child goes to the trash on its own, then the parent (with its
	// other child) is deleted and purged on dev1's clone. On dev2's clone
	// a plain restore of the child is refused (the parent is gone for
	// good); with --parent it comes back under the new home.
	rt.c1.MustLore("task", "delete", c1, "--yes")
	rt.c1.MustLore("task", "delete", p, "--yes")
	rt.c1.MustLore("trash", "purge", p, "--confirm")
	teASameTree(t, "after the purge", rt, map[string]string{p: "-", c1: "-", c2: "-", adopter: ""})
	if got := teATrashed(t, rt.c2); len(got) != 1 || got[c1] == "" {
		t.Fatalf("only the lone child is left in the trash on dev2's clone: %v", got)
	}
	rt.c2.Lore("trash", "restore", c1).Refused(t, "E_PARENT_PURGED", p)
	rt.c2.MustLore("trash", "restore", c1, "--parent", adopter)
	teASameTree(t, "after the restore under a new parent", rt, map[string]string{p: "-", c1: adopter, c2: "-", adopter: ""})
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		if left := teATrashed(t, c); len(left) != 0 {
			t.Fatalf("the trash is empty on %s: %v", c.Dir, left)
		}
	}

	// The purged parent never comes back, and both clones say so the same
	// way after their syncs: E_PURGED (DELETION 3.6, DQ4).
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		r := c.Lore("trash", "restore", p)
		teABugLivePurgeList.PendingOn(t, r, "E_NOT_FOUND", p, "was deleted (live")
		r.Refused(t, "E_PURGED", p)
	}
}
