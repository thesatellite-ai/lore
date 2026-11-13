//go:build userscen

package userscen

// campaign_sync_ci_test.go — the CI-side sync verbs and the moved
// spellings, through the real binary:
//
//   - ci-merge: a --base that names no commit ("nope", "-x") is
//     E_INVALID_INPUT with a hint and touches nothing (a typo answered
//     E_INTERNAL, exit 1); an up-to-date base says so; a dirty tree is
//     refused
//   - install-action: --dry-run writes no file, the run writes the
//     workflow once, a second run is "already up to date"
//   - fix-projects with one project: nothing to fix, twice
//   - the moved spellings (sync trash list/restore, sync purge) print the
//     new spelling and behave like it
//   - row move / move across stores: repo → kb → me with --yes, the
//     unconfirmed move asks first and moves nothing, a move into the store
//     the row is in is refused with a hint, a move back into a store the
//     row left is refused with a hint, the row keeps its id and body

import (
	"os"
	"path/filepath"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// camSyncWorkflow is the file `lore sync install-action` writes.
const camSyncWorkflow = ".github/workflows/lore-sync-merge.yml"

// TestCampaignSync_CIMergeAndAction covers ci-merge refusals and the
// install-action life cycle.
func TestCampaignSync_CIMergeAndAction(t *testing.T) {
	_, a, _ := camSyncPair(t)
	head := a.Head()
	for _, base := range []string{"nope", "-x", "origin/nope"} {
		camSyncRefuses(t, a, camSyncRefusal{[]string{"sync", "ci-merge", "--base", base}, "E_INVALID_INPUT", 20, "is not a commit in this repository"})
	}
	if a.Head() != head || a.Status() != "" {
		t.Fatalf("a refused ci-merge changes nothing: head %s → %s, status %q", head, a.Head(), a.Status())
	}
	a.MustLore("sync", "ci-merge", "--base", "origin/main").Says(t, "already merged")
	a.WriteFile("README.md", "dirty")
	camSyncRefuses(t, a, camSyncRefusal{[]string{"sync", "ci-merge", "--base", "origin/main"}, "E_INVALID_INPUT", 20, "the work tree has changes"})
	if err := os.Remove(filepath.Join(a.Dir, "README.md")); err != nil {
		t.Fatal(err)
	}

	a.MustLore("sync", "install-action", "--dry-run").Says(t, "lore-sync-merge")
	if _, ok := a.ReadFile(camSyncWorkflow); ok {
		t.Fatal("install-action --dry-run must write no file")
	}
	a.MustLore("sync", "install-action").Says(t, "created", filepath.FromSlash(camSyncWorkflow))
	first, _ := a.ReadFile(camSyncWorkflow)
	a.MustLore("sync", "install-action").Says(t, "already up to date")
	if again, _ := a.ReadFile(camSyncWorkflow); again != first {
		t.Fatal("a second install-action must not rewrite the workflow")
	}

	for i := 0; i < 2; i++ {
		a.MustLore("sync", "fix-projects").Says(t, "only one project; nothing to fix")
	}
}

// TestCampaignSync_MovedSpellings: the old sync trash / sync purge words
// name their new spelling and answer like it.
func TestCampaignSync_MovedSpellings(t *testing.T) {
	_, a, _ := camSyncPair(t)
	a.MustLore("sync", "trash", "list").Says(t, "lore trash --local", "this machine only")
	a.MustLore("trash", "list", "--local").Says(t, "this machine only")
	camSyncRefuses(t, a, camSyncRefusal{[]string{"sync", "trash", "restore", "1"}, "E_NOT_FOUND", 27, "no local copy #1"})
	camSyncRefuses(t, a, camSyncRefusal{[]string{"trash", "restore", "--local", "--target", "1"}, "E_NOT_FOUND", 27, "no local copy #1"})
	a.Lore("sync", "purge", "--archived-before", "1d").Fails(t, th.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]", "lore trash purge --archived-before")
	a.MustLore("sync", "purge", "--archived-before", "1d", "--confirm").Says(t, "purged 0 rows")
}

// TestCampaignSync_RowMoveAcrossStores walks one memory through the stores.
func TestCampaignSync_RowMoveAcrossStores(t *testing.T) {
	tw := newTrashWorld(t, true)
	c := tw.c
	body := "rotate the staging keys every quarter — ünï 🔑"
	id := trashID(t, c.MustLore("memory", "add", body, "--json"))
	c.Lore("row", "move", id, "--to", trashStoreKB).Fails(t, th.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]", "Move it?")
	if got := trashShow(t, c, id); got.Store != trashStoreRepo {
		t.Fatalf("an unconfirmed move moves nothing: in %s", got.Store)
	}
	for _, step := range []struct {
		verb []string
		to   string
	}{{[]string{"row", "move"}, trashStoreKB}, {[]string{"move"}, trashStoreMe}} {
		c.MustLore(append(append([]string{}, step.verb...), id, "--to", step.to, "--yes")...).Says(t, id, "moved to store "+step.to, "same id")
		got := trashShow(t, c, id)
		if got.Store != step.to || got.Fields["body"] != body {
			t.Fatalf("after the move to %s: store %s, body %q", step.to, got.Store, got.Fields["body"])
		}
	}
	camSyncRefuses(t, c, camSyncRefusal{[]string{"row", "move", id, "--to", trashStoreMe, "--yes"}, "E_INVALID_INPUT", 20, "already in store me"})
	for _, back := range []string{trashStoreRepo, trashStoreKB} {
		camSyncRefuses(t, c, camSyncRefusal{[]string{"move", id, "--to", back, "--yes"}, "E_UNSUPPORTED", th.ExitRefused, "a row never returns to a place it left"})
	}
	c.MustLore("row", "show", id, "--origin").Says(t, "moved")
}
