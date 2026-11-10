//go:build userscen

package userscen

// campaign_sync_migrate_test.go — `lore migrate run|status|rollback` on a
// team repo that holds both v0.1.17 row files (_v: 1, the shape a v0.1.17
// user's checkout has, made by migDowngrade) and today's open live rows:
//
//   - status counts the v1 files; the dry run names exactly them (never an
//     open live row) and writes nothing; the run rewrites exactly them and
//     never copies a live row into .lore/data (it did: every open live
//     task was "upgraded" into a branch file, bypassing land); the live
//     row stays live; a second run is a no-op
//   - rollback before the commit puts the v1 bytes back; a second rollback
//     is E_NOT_FOUND with a hint; run again, commit, push: the teammate's
//     clone needs nothing and reads every row

import (
	"os"
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignSync_MigrateOldFilesBesideLiveRows walks a migration of a
// two-person repo with v0.1.17 files and open live rows.
func TestCampaignSync_MigrateOldFilesBesideLiveRows(t *testing.T) {
	st, a, b := camSyncPair(t)
	kinds := []migKind{}
	for _, k := range migKinds() {
		if k.table == "memories" || k.table == "rules" {
			kinds = append(kinds, k)
		}
	}
	var planted []migPlanted
	for _, k := range kinds {
		id := migAdd(t, a, k)
		planted = append(planted, migPlanted{k: k, id: id})
	}
	for i := range planted {
		planted[i].v1 = migDowngrade(t, migRowPath(a, planted[i].k.table, planted[i].id))
	}
	a.CommitAll("rows as v0.1.17 wrote them")
	a.AdminPush("origin", "main")
	liveID := camSyncAddTask(t, st, a, "an open live task — not a v0.1.17 row")
	liveFile := migRowPath(a, "tasks", liveID)

	if p := migRepoPlace(t, migStatus(t, a)); p.V1 != len(planted) {
		t.Fatalf("status: %+v, want %d v1 rows", p, len(planted))
	}
	before := camSyncTree(t, a.Dir)
	dry := th.DecodeJSON[migRunOut](t, a.MustLore("migrate", "run", "--dry-run", "--json"))
	if dry.Count != len(planted) || slices.ContainsFunc(dry.Rows, func(r string) bool { return strings.Contains(r, liveID) }) {
		t.Fatalf("dry run: count %d rows %v, want the %d v1 rows and never the live task %s", dry.Count, dry.Rows, len(planted), liveID)
	}
	migWantRows(t, "dry run", dry.Rows, planted)
	camSyncSameTree(t, "migrate run --dry-run", before, camSyncTree(t, a.Dir))

	out := th.DecodeJSON[migRunOut](t, a.MustLore("migrate", "run", "--json"))
	if out.Count != len(planted) {
		t.Fatalf("run: count %d rows %v, want %d", out.Count, out.Rows, len(planted))
	}
	migWantRows(t, "run", out.Rows, planted)
	if _, err := os.Stat(liveFile); err == nil {
		t.Fatalf("migrate run copied the open live task into %s (it reaches .lore/data only by landing)", liveFile)
	}
	a.MustLore("live", "show").Says(t, liveID)
	if again := th.DecodeJSON[migRunOut](t, a.MustLore("migrate", "run", "--json")); again.Count != 0 || len(again.Rows) != 0 {
		t.Fatalf("second run: %+v, want nothing", again)
	}

	a.MustLore("migrate", "rollback")
	for _, p := range planted {
		if got := migRead(t, migRowPath(a, p.k.table, p.id)); string(got) != string(p.v1) {
			t.Fatalf("rollback puts the v0.1.17 bytes of %s back:\n got %s\nwant %s", p.id, got, p.v1)
		}
	}
	r := a.Lore("migrate", "rollback")
	r.Fails(t, th.ExitNotFound, "[E_NOT_FOUND]", "no migration to roll back")
	if camSyncHint(r) == "" {
		t.Fatalf("a second rollback names the next step:\n%s", r.Out())
	}

	if out := th.DecodeJSON[migRunOut](t, a.MustLore("migrate", "run", "--json")); out.Count != len(planted) {
		t.Fatalf("run after the rollback: %+v", out)
	}
	a.CommitAll("lore: migrate format")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	b.MustLore("migrate", "run", "--dry-run").Says(t, "Nothing to migrate")
	for _, p := range planted {
		b.MustLore(p.k.noun, "show", p.id).OK(t)
	}
	b.MustLore("task", "list").Says(t, liveID)
}
