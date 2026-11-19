//go:build stories && !windows

package stories

// O_whoami_reads_test.go — devco D-596 on the owner-shaped machine (story
// O): whoami in a repo linked to a store folder and inside a folder of the
// store clone printed "stores … (org folder org)" and "links known 1
// link(s) …", and never which place the repo reads. Now whoami has a
// "reads" row: the <org>/<folder> a linked repo reads, or the folder of the
// store clone the command runs in, and the org folder is named as a place
// (<org>/<folder>), not a bare folder name that reads like a word.

import (
	"testing"
)

func TestStoryOWhoamiNamesTheFolderItReads(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	wc := ada.CloneTo(web, ada.OrgDir("web"))
	ada.LoreIn(wc, "org", "link", "--org", oOrg, "--project", oFolder, "--yes").OK(t)

	place := oOrg + "/" + oFolder
	s.Step("D-596/linked-repo-names-its-folder", func() {
		r := ada.LoreIn(wc, "whoami").OK(t)
		r.Says(t, "reads      "+place+" (this repo's link)", "org folder "+oOrg+"/org: roster")
		r.Never(t, "(org folder org)")
	})
	s.Step("D-596/store-folder-names-itself", func() {
		dir := ada.LorePath("stores", oOwner, "lore-team", oFolder)
		r := ada.Lore(dir, "whoami").OK(t)
		r.Says(t, "reads      "+place+" (this folder of the store clone)")
		r.Never(t, "(org folder org)")
	})
}
