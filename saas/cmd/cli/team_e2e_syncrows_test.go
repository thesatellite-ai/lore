//go:build teame2e

package main

// team_e2e_syncrows_test.go — B11 ports of sync_e2e_test.go's row-level
// sync verbs, run against LORE_TEAM_BIN (cmd/lore): `lore sync
// fix-projects` folds two parallel bootstraps into one project, `lore sync
// merge-rows` folds a duplicate row (both over engine.MergeRows: referrers
// re-pointed, the dropped row soft-deleted "merged into <keep>"), and
// `lore sync trash` points at `lore trash --local` (P3-SYNC-T1/T2).

import (
	"encoding/json"
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// rowField reads one field of a row file in c (table/id under .lore/data).
func rowField(t *testing.T, c *th.Clone, table, id, field string) string {
	t.Helper()
	b, ok := c.ReadFile(dataDir + "/" + table + "/" + id + ".json")
	if !ok {
		t.Fatalf("no row file %s/%s", table, id)
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(b), &m); err != nil {
		t.Fatalf("row file %s/%s: %v", table, id, err)
	}
	var s string
	if raw, ok := m[field]; ok && json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	return s
}

// TestB11Sync_FixProjectsRepointsCoreRows (TestE2E_FixProjectsRepointsCoreRows):
// alice and bob bootstrap lore in parallel; bob's merge keeps alice's
// _meta.json; `sync fix-projects` keeps that project, re-points bob's rule
// to it and puts bob's project row in the trash ("merged into <keep>");
// a second run has nothing to fix.
func TestB11Sync_FixProjectsRepointsCoreRows(t *testing.T) {
	w := newTeamWorld(t)
	app := w.Remote(slugApp)
	alice := w.Human("alice", "alice@acme.example").Clone(app, "app")
	bob := w.Human("bob", "bob@acme.example").Clone(app, "app")
	alice.MustLore("init", "--non-interactive", "--name=app")
	aliceRule := addSyncRule(t, alice, "alice rule")
	alice.CommitAll("alice lore")
	alice.AdminPush("origin", "main")
	bob.MustLore("init", "--non-interactive", "--name=app")
	bobRule := addSyncRule(t, bob, "bob rule")
	bob.CommitAll("bob lore")
	// cmd/lore's merge driver may settle _meta.json itself; a clash is
	// settled as the old test did, keeping alice's file.
	if r := bob.TryGit("pull", "-q", "--no-rebase", "origin", "main"); r.Code != 0 {
		bob.Git("checkout", "-q", "--theirs", dataDir+"/"+metaFile)
		bob.CommitAll("merge")
	}
	keep := rowField(t, alice, "rules", aliceRule, "project_id")
	folded := rowField(t, bob, "rules", bobRule, "project_id")
	if keep == "" || keep == folded {
		t.Fatalf("setup: projects %q and %q", keep, folded)
	}
	bob.MustLore("sync", "fix-projects", "--dry-run").Says(t, "would keep "+keep, folded)
	if got := rowField(t, bob, "rules", bobRule, "project_id"); got != folded {
		t.Fatalf("a dry run re-pointed the rule to %s", got)
	}
	bob.MustLore("sync", "fix-projects").Says(t, "kept "+keep, folded)
	for _, id := range []string{aliceRule, bobRule} {
		if got := rowField(t, bob, "rules", id, "project_id"); got != keep {
			t.Fatalf("rule %s names project %s, want the kept %s", id, got, keep)
		}
	}
	if d := rowField(t, bob, "projects", folded, "deleted_reason"); d != "merged into "+keep {
		t.Fatalf("the folded project is not in the trash as merged: %q", d)
	}
	bob.MustLore("trash").Says(t, folded, "merged into "+keep)
	bob.MustLore("project", "list").Says(t, keep).Never(t, folded)
	bob.MustLore("sync", "fix-projects").Says(t, "nothing to fix")
}

// TestB11Sync_MergeRowsFoldsDuplicateTag (TestE2E_MergeRows): two tags,
// memories tagged with the duplicate (one with both); merge-rows keeps one
// tag on every memory, the duplicate goes to the trash and comes back with
// its batch; merging it again is E_IN_TRASH; a wrong table is refused.
func TestB11Sync_MergeRowsFoldsDuplicateTag(t *testing.T) {
	s := newSyncSolo(t)
	c := s.c
	keep := mustID(t, c.MustLore("tag", "add", "--name=deploy"), "tag")
	drop := mustID(t, c.MustLore("tag", "add", "--name=deploys"), "tag")
	both := mustID(t, c.MustLore("memory", "add", "--body", "both tags", "--tag", "deploys", "--tag", "deploy"), "mem")
	only := mustID(t, c.MustLore("memory", "add", "--body", "duplicate only", "--tag", "deploys"), "mem")
	c.Lore("sync", "merge-rows", "memories", "--keep", keep, "--drop", drop).Refused(t, "not an id of memories")
	r := c.MustLore("sync", "merge-rows", "tags", "--keep", keep, "--drop", drop).Says(t, "merged "+drop+" into "+keep)
	batch := mustID(t, r, "bat")
	for _, m := range []string{both, only} {
		b, _ := c.ReadFile(dataDir + "/memories/" + m + ".json")
		if strings.Contains(b, drop) || strings.Count(b, keep) != 1 {
			t.Fatalf("memory %s tags after the merge:\n%s", m, b)
		}
	}
	c.MustLore("tag", "list").Says(t, "deploy", "(2 uses)").Never(t, drop)
	c.MustLore("trash").Says(t, drop, "merged into "+keep, batch)
	c.Lore("sync", "merge-rows", "tags", "--keep", keep, "--drop", drop).Refused(t, "E_IN_TRASH")
	c.MustLore("trash", "restore", batch)
	c.MustLore("tag", "list").Says(t, drop)
}

// TestB11Sync_SyncTrashPointsAtTrashLocal (TestE2E_RevertTrashRestore,
// the sync trash half; DELETION 3.10, OV31-a): `lore sync trash list`
// prints the pointer at `lore trash --local` on stderr and the same empty
// listing; `sync trash restore <n>` points at `trash restore --local <n>`
// and gets its answer.
func TestB11Sync_SyncTrashPointsAtTrashLocal(t *testing.T) {
	c := newSyncSolo(t).c
	r := c.MustLore("sync", "trash", "list")
	if !strings.Contains(r.Stderr, "note: lore sync trash is now lore trash --local; this spelling goes away in the next release") {
		t.Fatalf("no pointer line on stderr: %s", r)
	}
	r.Says(t, "trash is empty")
	c.Lore("sync", "trash", "restore", "1").Refused(t, "lore trash restore --local 1", "no local copy #1")
}
