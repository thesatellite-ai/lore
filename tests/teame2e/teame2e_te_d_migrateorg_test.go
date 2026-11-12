//go:build teame2e

package teame2e

// later.md 288 (migrateOrg v1 roster rows) on cmd/lore across machines: a
// v0.1.17 roster row in the org folder of the team's store becomes `_v: 2`
// on one person's `lore migrate run` + `lore sync`, and every teammate's
// clone resolves its handle afterwards.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDV1Actor is a v0.1.17 roster row id (act_ + 32 hex).
const teDV1Actor = "act_01a1a5aca7f57659be7320c5f4b7d003"

// teDV1ActorDoc is dev3's roster row as v0.1.17 wrote it: `_v: 1`, flat
// columns, no `_hlc`.
const teDV1ActorDoc = `{"_table":"actors","_v":1,"active":true,"created_at":"2026-11-04T08:09:56.252002659Z","display_name":"Dev Three","handle":"dev3","id":"` +
	teDV1Actor + `","kind":"human","stable_key":"human:dev3@northwind.example","updated_at":"2026-11-04T08:09:56.252002894Z"}`

// TestTeD_MigrateOrgV1RosterRowReachesTeammates: alice's migrate run
// rewrites the org folder's v1 actors row to `_v: 2` (keeping its
// fields), leaves the 0.1.18 rows alone, `lore sync` pushes it, dev1's
// clone assigns @dev3, and a second run is a no-op.
func TestTeD_MigrateOrgV1RosterRowReachesTeammates(t *testing.T) {
	a := newAgency(t, th.Cmd("migrate", "run"), th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"))
	rowPath := folderOrg + "/" + tblActors + "/" + teDV1Actor + ".json"

	// The team's store as v0.1.17 left it: dev3 registered with the old
	// binary, so its row is `_v: 1`.
	work := a.alice.Clone(a.store, "store-work")
	work.WriteFile(rowPath, teDV1ActorDoc)
	work.CommitAll("lore: v0.1.17 roster row")
	work.AdminPush("origin", storeBranch)
	others := map[string]string{}
	for _, f := range a.store.FilesMatching(refMain, folderOrg+"/"+tblActors+"/") {
		if !strings.HasSuffix(f, teDV1Actor+".json") {
			body, _ := a.store.Read(refMain, f)
			others[f] = body
		}
	}
	if len(others) != 2 {
		t.Fatalf("the agency roster holds alice and dev1: %v", others)
	}

	a.aliceApp.MustLore("migrate", "run").Says(t, "org store", tblActors, "_v: 1 → _v: 2")
	a.aliceApp.MustLore("sync")
	body, ok := a.store.Read(refMain, rowPath)
	if !ok {
		t.Fatalf("dev3's row is gone from the store: %v", a.store.Files(refMain))
	}
	compact := strings.Join(strings.Fields(body), "")
	for _, want := range []string{`"_v":2`, `"_hlc":`, `"handle":"dev3"`, `"display_name":"DevThree"`, `"stable_key":"human:dev3@northwind.example"`} {
		if !strings.Contains(compact, want) {
			t.Fatalf("the migrated row lacks %s: %s", want, body)
		}
	}
	for f, before := range others {
		if after, _ := a.store.Read(refMain, f); after != before {
			t.Fatalf("a 0.1.18 roster row is not rewritten: %s\n%s\n%s", f, before, after)
		}
	}

	// dev1, on his own machine, never ran migrate: his next command sees
	// dev3 on the roster.
	id := mustID(t, a.dev1App.MustLore("task", "add", "pair with dev3", "--to", "repo", "--tasklist", a.tl, "--assign", "@dev3"), "tsk")
	a.dev1App.MustLore("task", "show", id).Says(t, "Dev Three")

	a.aliceApp.MustLore("migrate", "run").Never(t, "_v: 1 → _v: 2")
	if again, _ := a.store.Read(refMain, rowPath); again != body {
		t.Fatalf("a second run rewrites nothing:\n%s\n%s", body, again)
	}
}
