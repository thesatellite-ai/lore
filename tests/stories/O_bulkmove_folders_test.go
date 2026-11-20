//go:build stories && !windows

package stories

// O_bulkmove_folders_test.go — devco:D-761 (bug hunt H-20) on the
// owner-shaped machine (story O): with the org setup in place, the admin
// keeps notes in folders of a personal store and moves one folder's rows on
// twice, a -> b then b -> c, from an org repo. The second move failed
// E_NOT_FOUND "refers to rows no attached store holds: project_id" (the
// first left every row on folder a's project) while its --dry-run passed,
// and the first "left tasklist … behind" for the task although its list
// moved in the same batch. Each moved row now takes the target folder's
// project, the task keeps its list, and the dry run refuses what the move
// refuses.

import (
	"strings"
	"testing"
)

func TestStoryOBulkMoveBetweenPersonalFolders(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	notes := s.Remote("github.com/" + oAdminPersonal.Login + "/notes")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	var taskID string

	s.Step("D-761/personal-store-with-three-folders", func() {
		ada.Lore(pc.Dir, "store", "init", "--name", "me", "--url", notes.SSHURL(), "--yes").OK(t)
		for _, f := range []string{"a", "b", "c"} {
			ada.Lore(pc.Dir, "store", "folder", "add", "--store", "me", "--folder", f).OK(t)
		}
		ada.Lore(pc.Dir, "memory", "add", "--to", "me/a", "--body", "rounding happens at the line").OK(t)
		if taskID = oAddedID(t, ada.Lore(pc.Dir, "task", "add", "--to", "me/a", "--title", "check the invoice totals", "--json").OK(t)); taskID == "" {
			t.Fatal("task add --json printed no id")
		}
	})

	s.Step("D-761/a-to-b-then-b-to-c", func() {
		ada.Lore(pc.Dir, "move", "--from", "me/a", "--to", "me/b", "--yes").OK(t).
			Says(t, "✓ moved 3 row(s) from me/a to me/b").Never(t, "left tasklist")
		ada.Lore(pc.Dir, "move", "--from", "me/b", "--to", "me/c", "--dry-run").OK(t).
			Says(t, "would move 3 row(s) from me/b to me/c", "next: lore move --from me/b --to me/c --yes")
		ada.Lore(pc.Dir, "move", "--from", "me/b", "--to", "me/c", "--yes").OK(t).
			Says(t, "✓ moved 3 row(s) from me/b to me/c").Never(t, "E_NOT_FOUND", "left tasklist")
		shown := ada.Lore(pc.Dir, "show", "--id", taskID, "--store", "me/c", "--json").OK(t).Stdout
		if !strings.Contains(shown, `"tasklist_id": "tlt_`) {
			t.Errorf("the task lost its list after two folder moves:\n%s", shown)
		}
	})
}
