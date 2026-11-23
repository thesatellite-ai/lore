//go:build stories && !windows

package stories

// O_move_out_of_linked_folder_test.go — devco:D-923 (bug hunt H-181, the
// owner's 440-action import, item 3) on the owner-shaped machine (story
// O): in a repo linked to a folder of the org store, `lore row move --id X
// --to <org>/<other folder>` succeeded, then `lore meta set --id X` (and
// show, edit, archive by id) from the same repo answered "X was deleted
// from <org>": the tombstone the move leaves in the linked folder stopped
// the by-id search before the org's other folders were looked in. Now the
// move says the row leaves this repo's lists and names the read that still
// reaches it, and every command by id finds it in the folder it moved to.

import (
	"testing"
)

func TestStoryOByIDAfterMoveOutOfLinkedFolder(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote("github.com/"+oOwner+"/shop-api"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	sc := ada.CloneTo(shop, ada.OrgDir("shop-api"))
	linked, other := oOrg+"/product", oOrg+"/billing"
	var mem string

	s.Step("D-923/a-repo-linked-to-a-folder", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "product").OK(t)
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "billing").OK(t)
		ada.Lore(sc.Dir, "init", "--non-interactive", "--name=shop-api").OK(t)
		ada.Lore(sc.Dir, "store", "adopt", "--target", linked, "--move", "--commit", "--yes").OK(t)
		ada.Lore(sc.Dir, "meta", "add", "--name", "owner", "--type", "text", "--to", linked, "--yes").OK(t)
		mem = idAfter(t, ada.Lore(sc.Dir, "memory", "add", "--body", "the invoice queue drains every ten minutes", "--json").OK(t).Stdout, "id", "mem_")
	})

	s.Step("D-923/move-says-how-to-reach-it", func() {
		ada.Lore(sc.Dir, "row", "move", "--id", mem, "--to", other, "--yes").OK(t).
			Says(t, "every command by id still finds a row there", "lore show --store "+other+" --id "+mem)
	})

	s.Step("D-923/every-command-by-id-finds-it", func() {
		ada.Lore(sc.Dir, "meta", "set", "--id", mem, "--pair", "owner=dana").OK(t).Never(t, "was deleted from")
		ada.Lore(sc.Dir, "show", "--id", mem).OK(t).Says(t, other, "owner: dana")
		ada.Lore(sc.Dir, "show", "--store", other, "--id", mem).OK(t).Says(t, "owner: dana")
		ada.Lore(sc.Dir, "memory", "edit", "--id", mem, "--body", "the invoice queue drains every hour").OK(t)
		ada.Lore(sc.Dir, "memory", "archive", "--id", mem, "--yes").OK(t)
		ada.Lore(sc.Dir, "show", "--id", mem).OK(t).Says(t, "every hour", "archived_at")
	})
}
