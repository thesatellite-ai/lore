//go:build stories && !windows

package stories

// O_move_folder_orgstore_test.go — devco:D-992 (bug hunt H-260) on the
// owner-shaped machine (story O): the admin keeps a client's notes in a
// folder of the org's main store and adds a second store to the org for
// that client. Moving the folder into the new store under its own name
// failed: `lore move --from <org>/shop --to lore-ops/shop` resolved the
// target by folder name to the folder it was leaving ("same store;
// nothing to move"), and `lore store folder add` on the new store refused
// the name (folder names are unique across an org's stores). Now the move
// carries the folder: its rows and settings, its repo link, the folder made
// in the new store and the old one removed; the refusal of folder add
// prints that move. Every name, email and url is made up.

import (
	"strings"
	"testing"
)

func TestStoryOMoveFolderIntoOrgStore(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	ops := s.Remote("github.com/" + oOwner + "/lore-ops")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	scratch := s.Seed(s.Remote("github.com/"+oOwner+"/scratch"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	var memID string

	s.Step("D-992/a-client-folder-in-the-main-store-and-a-second-store", func() {
		sc := ada.CloneTo(scratch, ada.OrgDir("scratch"))
		ada.LoreIn(sc, "store", "init", "--name", "ops", "--url", ops.SSHURL(), "--yes").OK(t)
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", ops.SSHURL(), "--push", "--yes").OK(t)
		ada.LoreIn(pc, "store", "folder", "add", "--store", oOrg, "--folder", "shop").OK(t)
		if memID = oAddedID(t, ada.LoreIn(pc, "memory", "add", "--to", oOrg+"/shop", "--body", "the shop deploys on fridays", "--yes", "--json").OK(t)); memID == "" {
			t.Fatal("memory add --json printed no id")
		}
		ada.LoreIn(pc, "task", "add", "--to", oOrg+"/shop", "--title", "ship the order page", "--yes").OK(t)
		ada.LoreIn(pc, "org", "link", "--org", oOrg, "--project", "shop", "--yes").OK(t)
	})

	s.Step("D-992/folder-add-on-the-second-store-prints-the-move", func() {
		ada.LoreIn(pc, "store", "folder", "add", "--store", "lore-ops", "--folder", "shop").
			Refused(t, "folder names are unique across an org's stores", "lore move --from "+oOrg+"/shop --to lore-ops/shop --dry-run")
	})

	s.Step("D-992/the-dry-run-plans-the-folder-move", func() {
		ada.LoreIn(pc, "move", "--from", oOrg+"/shop", "--to", "lore-ops/shop", "--dry-run").OK(t).
			Says(t, "would move 3 row(s) from "+oOrg+"/shop to lore-ops/shop",
				"would move folder shop from "+oOrg+"/shop to lore-ops/shop", "1 repo link(s)",
				"next: lore move --from "+oOrg+"/shop --to lore-ops/shop --yes").
			Never(t, "same store", "nothing to move")
		ada.LoreIn(pc, "move", "--from", oOrg+"/shop", "--to", "lore-ops/shop", "--where", "table=memories", "--dry-run").
			Refused(t, "takes the whole folder")
	})

	s.Step("D-992/the-move-carries-the-folder", func() {
		ada.LoreIn(pc, "move", "--from", oOrg+"/shop", "--to", "lore-ops/shop", "--yes").OK(t).
			Says(t, "✓ moved 3 row(s) from "+oOrg+"/shop to lore-ops/shop", "✓ moved folder shop to lore-ops/shop", oOrg+"/shop removed")
		ada.LoreIn(pc, "sync").OK(t)
		meta, _ := ops.Read("refs/heads/main", "shop/_meta.json")
		oCheck(t, "D-992/new-folder-with-link", strings.Contains(meta, `"remote": "github.com/`+oOwner+`/platform"`),
			"the new store's shop/_meta.json does not carry the repo link:\n%s", meta)
		old, _ := team.Read("refs/heads/main", "shop/_meta.json")
		oCheck(t, "D-992/old-folder-gone", old == "", "the main store still has shop/_meta.json:\n%s", old)
		row, _ := ops.Read("refs/heads/main", "shop/memories/"+memID+".json")
		oCheck(t, "D-992/row-in-new-store", strings.Contains(row, "the shop deploys on fridays"), "the moved note is not in the new store's shop/:\n%s", row)
		// this repo is linked to the folder, which now lives in the new store
		ada.LoreIn(pc, "show", "--id", memID).OK(t).Says(t, "the shop deploys on fridays")
		ada.LoreIn(pc, "store", "folder", "list").OK(t).Says(t, "shop")
		ada.LoreIn(pc, "org", "link", "--org", oOrg, "--project", "shop", "--yes").OK(t).Says(t, "lore-ops", "already so")
	})
}
