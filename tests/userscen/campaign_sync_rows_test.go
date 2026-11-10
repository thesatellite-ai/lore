//go:build userscen

package userscen

// campaign_sync_rows_test.go — the row-level sync verbs between two
// people, through the real binary:
//
//   - promote: --dry-run changes no ref and no file, the run commits the
//     rule onto main (not pushed), a second promote commits nothing, peek
//     lists it on main; a value that is no row id ("../x") is E_INVALID_ID
//     before any path is built (it read .lore/<x>.json and tried to commit
//     it), a missing row and a missing branch are E_NOT_FOUND, each with a
//     hint
//   - merge-rows: the duplicate goes to the trash, a second merge is
//     E_IN_TRASH, keep = drop and a table that does not match the ids are
//     refused with a hint, restoring the duplicate brings it back
//   - conflicts: both people edit the same field; after the pull the
//     conflict is listed once, resolve --take other keeps the teammate's
//     text, a second resolve is E_NOT_FOUND with a hint

import (
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignSync_PromoteAndPeek: an urgent rule from a feature branch
// onto main.
func TestCampaignSync_PromoteAndPeek(t *testing.T) {
	_, a, _ := camSyncPair(t)
	a.Git("checkout", "-q", "-b", "feature")
	rule := trashID(t, a.MustLore("rule", "add", "Never force-push main — 🚫", "--json"))
	a.WriteFile(".lore/evil.json", `{"x":1}`)
	mainRev := a.LocalRef("refs/heads/main")
	before := camSyncTree(t, a.Dir)
	a.MustLore("sync", "promote", rule, "--to", "main", "--dry-run").Says(t, "would commit", rule)
	if a.LocalRef("refs/heads/main") != mainRev {
		t.Fatal("promote --dry-run must not move main")
	}
	camSyncSameTree(t, "promote --dry-run", before, camSyncTree(t, a.Dir))

	a.MustLore("sync", "promote", rule, "--to", "main").Says(t, "onto main", "not pushed")
	if a.LocalRef("refs/heads/main") == mainRev {
		t.Fatal("promote commits onto main")
	}
	if got := a.Git("show", "main:.lore/data/rules/"+rule+".json"); !strings.Contains(got, rule) {
		t.Fatalf("main holds the promoted rule file:\n%s", got)
	}
	mainRev = a.LocalRef("refs/heads/main")
	a.MustLore("sync", "promote", rule, "--to", "main").Says(t, "already holds these rows")
	if a.LocalRef("refs/heads/main") != mainRev {
		t.Fatal("a second promote must commit nothing")
	}
	a.MustLore("sync", "peek", "main", "rules").Says(t, rule)

	for _, tc := range []camSyncRefusal{
		{[]string{"sync", "promote", "../../evil", "--to", "main"}, "E_INVALID_ID", 20, "is not a row id"},
		{[]string{"sync", "promote", "../evil", "--to", "main", "--dry-run"}, "E_INVALID_ID", 20, "is not a row id"},
		{[]string{"sync", "promote", "rul_01a1b7d502a673b1b4b27e1c03466461", "--to", "main"}, "E_NOT_FOUND", 27, "no .lore/data file"},
		{[]string{"sync", "promote", rule, "--to", "nope"}, "E_NOT_FOUND", 27, "branch nope not found"},
	} {
		camSyncRefuses(t, a, tc)
	}
	if a.LocalRef("refs/heads/main") != mainRev {
		t.Fatal("a refused promote must not move main")
	}
}

// TestCampaignSync_MergeRows folds one duplicate memory into another.
func TestCampaignSync_MergeRows(t *testing.T) {
	_, a, _ := camSyncPair(t)
	keep := trashID(t, a.MustLore("memory", "add", "deploys happen on Tuesdays", "--no-live", "--json"))
	drop := trashID(t, a.MustLore("memory", "add", "we deploy on Tuesday", "--no-live", "--json"))
	a.MustLore("sync", "merge-rows", "memories", "--keep", keep, "--drop", drop).Says(t, "merged "+drop+" into "+keep, "in the trash")
	if _, in := trashByID(trashList(t, a))[drop]; !in {
		t.Fatal("the dropped duplicate is in the trash")
	}
	a.MustLore("memory", "list").Says(t, keep).Never(t, drop)
	a.Lore("sync", "merge-rows", "memories", "--keep", keep, "--drop", drop).Fails(t, th.ExitRefused, "[E_IN_TRASH]", "lore trash restore")
	for _, tc := range []camSyncRefusal{
		{[]string{"sync", "merge-rows", "memories", "--keep", keep, "--drop", keep}, "E_INVALID_INPUT", 20, "the same row"},
		{[]string{"sync", "merge-rows", "tasks", "--keep", keep, "--drop", drop}, "E_INVALID_INPUT", 20, "is not an id of tasks"},
		{[]string{"sync", "merge-rows", "nope", "--keep", keep, "--drop", drop}, "E_INVALID_INPUT", 20, "is not a lore table"},
	} {
		camSyncRefuses(t, a, tc)
	}
	a.MustLore("trash", "restore", drop)
	a.MustLore("memory", "list").Says(t, keep, drop)
}

// TestCampaignSync_ConflictResolve: both people edit one field; the
// conflict is settled once with the teammate's text.
func TestCampaignSync_ConflictResolve(t *testing.T) {
	_, a, b := camSyncPair(t)
	id := trashID(t, a.MustLore("memory", "add", "the release train leaves at 10", "--no-live", "--json"))
	a.CommitAll("note")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	b.MustLore("memory", "edit", id, "--body", "the release train leaves at 11 (bob)")
	b.CommitAll("bob edits")
	b.AdminPush("origin", "main")
	a.MustLore("memory", "edit", id, "--body", "the release train leaves at 9 (alice)")
	a.CommitAll("alice edits")
	a.TryGit("pull", "-q", "--no-rebase", "origin", "main")

	type conflicts struct {
		Data []struct {
			ID  int    `json:"id"`
			Row string `json:"row"`
		} `json:"data"`
	}
	got := th.DecodeJSON[conflicts](t, a.MustLore("sync", "conflicts", "--json"))
	if len(got.Data) != 1 {
		t.Fatalf("one open conflict after the pull: %+v\n%s", got, a.Status())
	}
	n := got.Data[0].ID
	camSyncRefuses(t, a, camSyncRefusal{[]string{"sync", "resolve", "1", "--take", "bogus"}, "E_INVALID_INPUT", 20, "--take must be kept or other"})
	a.MustLore("sync", "resolve", strconv.Itoa(n), "--take", "other").Says(t, "resolved", "took other")
	a.MustLore("memory", "show", id).Says(t, "(bob)").Never(t, "(alice)")
	camSyncRefuses(t, a, camSyncRefusal{[]string{"sync", "resolve", strconv.Itoa(n), "--take", "other"}, "E_NOT_FOUND", 27, "no open conflict"})
	a.MustLore("sync", "conflicts").Says(t, "no conflicts")
}
