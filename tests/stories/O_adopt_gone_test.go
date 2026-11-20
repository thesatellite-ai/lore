//go:build stories && !windows

package stories

// O_adopt_gone_test.go — devco:D-737 on the owner-shaped machine (story
// O): an own repo whose open tasks were on its live ref. The owner deleted
// them, ran `lore trash purge --confirm` and `lore live purge --confirm`,
// then moved the repo's notes into a store folder: `lore store adopt`
// still counted them ("from the repo's live ref: 38"), listed them as
// near-duplicates and copied them; only `lore live compact` made adopt see
// them gone. Cells: the dry run counts only the open task, names the gone
// ones as never adopted and lists no near-duplicate for them; the move
// copies only the open task into the folder.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAdoptSkipsGoneLiveRows(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oOwner+"/billing-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	bc := ada.CloneTo(billing, ada.OrgDir("billing-app"))
	const folder = "billing"
	clone := oStoreClone(ada, oOwner, "lore-team")
	titles := []string{"rotate the billing token", "archive the old invoices", "check the refund job", "renew the billing domain"}
	ids := map[string]string{}
	taskID := func(out string) string {
		i := strings.Index(out, `"id": "tsk_`)
		if i < 0 {
			t.Fatalf("task add printed no id:\n%s", out)
		}
		return out[i+len(`"id": "`) : i+len(`"id": "`)+36]
	}

	s.Step("D-737/open-live-tasks-then-deleted-and-purged", func() {
		ada.Lore(bc.Dir, "init", "--non-interactive", "--name=billing-app").OK(t)
		bc.CommitAll("lore init")
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(bc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		for _, title := range titles {
			ids[title] = taskID(ada.Lore(bc.Dir, "task", "add", "--title", title, "--commitment", "accepted", "--to", "repo", "--json").OK(t).Stdout)
		}
		ada.Lore(bc.Dir, "sync").OK(t)
		ada.Lore(bc.Dir, "task", "delete", "--id", ids[titles[0]], "--yes").OK(t)
		ada.Lore(bc.Dir, "task", "delete", "--id", ids[titles[1]], "--yes").OK(t)
		ada.Lore(bc.Dir, "trash", "purge", "--id", ids[titles[1]], "--confirm").OK(t)
		ada.Lore(bc.Dir, "live", "purge", "--ids", ids[titles[2]], "--confirm", "--yes").OK(t) // not pushed yet
	})

	gone := titles[:3]
	s.Step("D-737/adopt-dry-run-counts-only-the-open-task", func() {
		r := ada.Lore(bc.Dir, "store", "adopt", "--target", oOrg+"/"+folder, "--move", "--own", "--dry-run").OK(t)
		bad := ""
		for _, title := range gone {
			if strings.Contains(r.Out(), ids[title]) || strings.Contains(r.Out(), title) {
				bad += " " + title
			}
		}
		oCheck(t, "D-737/adopt-dry-run-gone", bad == "" && strings.Contains(r.Out(), "tasks 1") &&
			strings.Contains(r.Out(), "tasks: 2 rows (deleted, in the trash or purged: never adopted)"),
			"adopt --dry-run counts or names deleted/purged live tasks (%s):\n%s", bad, r.Out())
	})

	s.Step("D-737/adopt-move-copies-only-the-open-task", func() {
		ada.Lore(bc.Dir, "store", "adopt", "--target", oOrg+"/"+folder, "--move", "--own", "--yes").OK(t)
		got := map[string]bool{}
		for _, title := range titles {
			got[title] = dirHolds(t, filepath.Join(clone, folder, "tasks"), title)
		}
		oCheck(t, "D-737/adopt-move-gone", got[titles[3]] && !got[titles[0]] && !got[titles[1]] && !got[titles[2]],
			"the folder's tasks after adopt --move (true = copied): %v", got)
	})
}
