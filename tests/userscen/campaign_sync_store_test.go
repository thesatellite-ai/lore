//go:build userscen

package userscen

// campaign_sync_store_test.go — the machine-side store commands after a
// store leaves a repo, the gaps sync_store_test.go leaves:
//
//   - store forget: by name and by url, for a git store (its clone under
//     ~/.lore/stores goes; a second forget says nothing to forget) and for
//     a folder store (file://: no clone, nothing to forget — it answered
//     "not a store url" for a url store add had accepted); a malformed url
//     is E_INVALID_INPUT with a hint
//   - cache prune after `store remove`: --dry-run lists the orphan cache
//     and deletes nothing, the plain run asks first (E_NEEDS_CONFIRM),
//     --yes removes it, a second run has nothing to prune
//   - store doctor and store show --json are one JSON object each, twice
//     the same

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// camSyncPruneOut is `lore cache prune --json`.
type camSyncPruneOut struct {
	Files []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"files"`
	Count   int  `json:"count"`
	DryRun  bool `json:"dry_run"`
	Removed bool `json:"removed"`
}

// TestCampaignSync_StoreForget: forget by name and url, git and folder.
func TestCampaignSync_StoreForget(t *testing.T) {
	st := newSyncTeam(t, 1)
	a, alice := st.clones[0], st.people[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	folder := fileuri.URL(filepath.Join(st.w.Root, "shared-folder-store"), "")
	a.MustLore("store", "init", "shelf", folder, "--yes")
	a.MustLore("memory", "add", "a row in the git store", "--to", "notes")
	a.MustLore("memory", "add", "a row in the folder store", "--to", "shelf")

	if clones := alice.StoreClones(t); len(clones) != 1 || !strings.HasPrefix(clones[0], alice.LorePath("stores")) {
		t.Fatalf("the git store has one clone under ~/.lore/stores: %v", clones)
	}
	a.MustLore("store", "forget", "notes").Says(t, "forgot")
	a.MustLore("store", "forget", r.URL()).Says(t, "nothing to forget")
	for _, args := range [][]string{{"store", "forget", "shelf"}, {"store", "forget", folder}} {
		out := a.MustLore(args...)
		out.Says(t, "nothing to forget").Never(t, "not a store url")
		if _, err := os.Stat(filepath.Join(st.w.Root, "shared-folder-store")); err != nil {
			t.Fatalf("lore %v must never touch the folder store itself: %v", args, err)
		}
	}
	camSyncRefuses(t, a, camSyncRefusal{args: []string{"store", "forget", "not a url"}, code: "E_INVALID_INPUT", exit: 20, says: "not a store url"})
	// The forgotten clone comes back on the next read; the rows are intact.
	a.MustLore("memory", "list").Says(t, "a row in the git store", "a row in the folder store")
}

// TestCampaignSync_CachePruneAfterRemove: a removed store's cache is
// listed, asked about, removed once.
func TestCampaignSync_CachePruneAfterRemove(t *testing.T) {
	st := newSyncTeam(t, 1)
	a, alice := st.clones[0], st.people[0]
	a.MustLore("store", "init", "notes", st.w.Remote(syncSlugStore).URL(), "--yes")
	a.MustLore("memory", "add", "cached row", "--to", "notes")
	empty := th.DecodeJSON[camSyncPruneOut](t, a.MustLore("cache", "prune", "--dry-run", "--json"))
	if empty.Count != 0 {
		t.Fatalf("an attached store's cache is never pruned: %+v", empty)
	}
	a.MustLore("store", "remove", "notes", "--yes")

	home := alice.LorePath()
	before := camSyncTree(t, home)
	dry := th.DecodeJSON[camSyncPruneOut](t, a.MustLore("cache", "prune", "--dry-run", "--json"))
	if dry.Count == 0 || dry.Removed || !dry.DryRun || len(dry.Files) != dry.Count {
		t.Fatalf("cache prune --dry-run lists the removed store's cache: %+v", dry)
	}
	for _, f := range dry.Files {
		if _, err := os.Stat(f.Path); err != nil || f.Size <= 0 {
			t.Errorf("listed cache file %s (%d bytes) must exist: %v", f.Path, f.Size, err)
		}
	}
	camSyncSameTree(t, "cache prune --dry-run", before, camSyncTree(t, home))
	a.Lore("cache", "prune").Fails(t, th.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]")
	done := th.DecodeJSON[camSyncPruneOut](t, a.MustLore("cache", "prune", "--yes", "--json"))
	if !done.Removed || done.Count != dry.Count {
		t.Fatalf("cache prune --yes removes what the dry run listed: %+v vs %+v", done, dry)
	}
	for _, f := range dry.Files {
		if _, err := os.Stat(f.Path); err == nil {
			t.Errorf("pruned cache file %s is still there", f.Path)
		}
	}
	again := th.DecodeJSON[camSyncPruneOut](t, a.MustLore("cache", "prune", "--yes", "--json"))
	if again.Count != 0 || again.Removed {
		t.Fatalf("a second prune has nothing to do: %+v", again)
	}
	a.MustLore("cache", "prune").Says(t, "nothing to prune")
}

// TestCampaignSync_StoreDoctorShowJSON: the machine-wide store reads are
// one JSON object each, the same twice.
func TestCampaignSync_StoreDoctorShowJSON(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	a.MustLore("store", "init", "notes", st.w.Remote(syncSlugStore).URL(), "--yes")
	for _, args := range [][]string{{"store", "doctor", "--json"}, {"store", "show", "--json"}, {"store", "show", "--links", "--json"}, {"sync", "status", "--json"}} {
		one := a.MustLore(args...)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(one.Stdout), &doc); err != nil || len(doc) == 0 {
			t.Fatalf("lore %v: one JSON object: %v\n%s", args, err, one.Stdout)
		}
		if two := a.MustLore(args...); !restJSONEqual([]byte(one.Stdout), []byte(two.Stdout)) {
			t.Errorf("lore %v twice: the same document\n%s\n%s", args, one.Stdout, two.Stdout)
		}
	}
}
