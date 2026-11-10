//go:build userscen

package userscen

// campaign_sync_trash_test.go — the trash reverse path across stores, the
// way a person walks it, as a matrix of store (repo, me, kb) × body (plain,
// unicode/emoji/RTL, 10k characters):
//
//	add → show → delete → trash list/show/stats → restore → restore again
//	(refused, with a hint that says how to read the live row) → delete →
//	purge without --confirm (E_NEEDS_CONFIRM) → purge --dry-run (the tree is
//	unchanged, byte for byte) → purge → purge again and restore (E_PURGED,
//	exit 23, with a hint) → the purged id is in no list, its store keeps a
//	tombstone; the trash list --to filter and stats agree after each step.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// camSyncBodies are the odd bodies of the trash matrix.
var camSyncBodies = []struct{ name, body string }{
	{"plain", "chase the unpaid invoice"},
	{"unicode", "Café ünï — 日本 🚀 שלום עולם <b>&</b> \"q\""},
	{"long", strings.Repeat("ab", 5000)},
}

// camSyncTrashStats is `lore trash stats --json`, the per-store counts.
type camSyncTrashStats struct {
	Rows    int `json:"rows"`
	ByStore []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"by_store"`
}

// TestCampaignSync_TrashReversePathAcrossStores walks the full trash life
// of a memory in every store with every body shape.
// One World per store, run in parallel (the bodies of one store share it).
func TestCampaignSync_TrashReversePathAcrossStores(t *testing.T) {
	scenParallel(t)
	for _, store := range trashStores {
		t.Run(store, func(t *testing.T) {
			scenParallel(t)
			camSyncTrashWalk(t, store)
		})
	}
}

// camSyncTrashWalk is the reverse path of every body shape in one store.
func camSyncTrashWalk(t *testing.T, store string) {
	tw := newTrashWorld(t, true)
	c := tw.c
	for _, b := range camSyncBodies {
		t.Run(b.name, func(t *testing.T) {
			id := trashID(t, c.MustLore("memory", "add", b.body, "--to", store, "--json"))
			if got := trashShow(t, c, id); got.Fields["body"] != b.body {
				t.Fatalf("show before delete: body %d chars, want %d", len(got.Fields["body"]), len(b.body))
			}
			c.MustLore("memory", "delete", id, "--yes")

			it, ok := trashByID(trashList(t, c, "--to", store))[id]
			if !ok || it.Store != store || it.Table != "memories" {
				t.Fatalf("trash list --to %s: %+v (found %v)", store, it, ok)
			}
			if _, other := trashByID(trashList(t, c, "--to", camSyncOtherStore(store)))[id]; other {
				t.Fatalf("trash list --to %s shows a row of %s", camSyncOtherStore(store), store)
			}
			sh := th.DecodeJSON[struct {
				Data   trashItem         `json:"data"`
				Fields map[string]string `json:"fields"`
			}](t, c.MustLore("trash", "show", id, "--json"))
			if sh.Data.ID != id || sh.Data.Store != store || sh.Fields["body"] != b.body {
				t.Fatalf("trash show: %+v body %d chars", sh.Data, len(sh.Fields["body"]))
			}

			res := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "restore", id, "--json"))
			if res.Count != 1 || len(res.Rows) != 1 || res.Rows[0].ID != id || res.Rows[0].Store != store {
				t.Fatalf("restore: %+v", res)
			}
			if got := trashShow(t, c, id); got.Fields["body"] != b.body {
				t.Fatal("a restored row keeps its body byte for byte")
			}
			again := c.Lore("trash", "restore", id)
			again.Fails(t, 27, "[E_NOT_FOUND]", "is not in the trash", "alive in "+store)
			if h := camSyncHint(again); !strings.Contains(h, "lore row show "+id) {
				t.Fatalf("restore of a live row: the hint says how to read it, got %q", h)
			}

			c.MustLore("memory", "delete", id, "--yes")
			c.Lore("trash", "purge", id).Fails(t, th.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]", "for good")
			home := tw.a.LorePath()
			beforeRepo, beforeHome := camSyncTree(t, c.Dir), camSyncTree(t, home)
			dry := th.DecodeJSON[trashResultOut](t, c.MustLore("trash", "purge", id, "--confirm", "--dry-run", "--json"))
			if !dry.DryRun || dry.Count != 1 {
				t.Fatalf("purge --dry-run: %+v", dry)
			}
			camSyncSameTree(t, "purge --dry-run (repo)", beforeRepo, camSyncTree(t, c.Dir))
			camSyncSameTree(t, "purge --dry-run (~/.lore)", beforeHome, camSyncTree(t, home))
			if _, still := trashByID(trashList(t, c))[id]; !still {
				t.Fatal("after a dry run the row is still in the trash")
			}
			c.MustLore("trash", "purge", id, "--confirm").Says(t, id, "tombstones are kept forever")
			for _, args := range [][]string{{"trash", "purge", id, "--confirm"}, {"trash", "restore", id}} {
				r := c.Lore(args...)
				r.Fails(t, 23, "[E_PURGED]", "purged rows never come back")
				if camSyncHint(r) == "" {
					t.Fatalf("lore %s: E_PURGED names the next step", strings.Join(args, " "))
				}
			}
			if _, still := trashByID(trashList(t, c))[id]; still {
				t.Fatal("a purged row is not in the trash list")
			}
			trashAssertPurgedShow(t, c, id)
		})
	}
	// After every purge the trash is empty and stats agree with the list.
	st := th.DecodeJSON[camSyncTrashStats](t, c.MustLore("trash", "stats", "--json"))
	if l := trashList(t, c); st.Rows != l.Count || l.Count != 0 || len(st.ByStore) != 0 {
		t.Fatalf("trash stats rows %d, list %d: both 0 after every purge", st.Rows, l.Count)
	}
}

// camSyncOtherStore is a store of the matrix other than s.
func camSyncOtherStore(s string) string {
	if s == trashStoreRepo {
		return trashStoreKB
	}
	return trashStoreRepo
}
