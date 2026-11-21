//go:build stories && !windows

package stories

// O_move_id_neardup_test.go — devco:D-854 (owner org-setup item 13, bug
// hunt H-111) on the owner-shaped machine (story O): the admin keeps notes
// in folders of a personal store and moves notes one at a time from an
// org repo. `lore move --from` and `lore store adopt` listed a moved rule
// that repeats one the folder already held; `lore move --id` (and `lore
// memory move`) moved the same rule in silently. Each now lists the pair
// with the commands that compare them and drop one, both kept; each
// printed command runs where printed.

import (
	"strings"
	"testing"
)

func TestStoryOMoveIDListsNearDuplicates(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	notes := s.Remote("github.com/" + oAdminPersonal.Login + "/notes")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	var curated, copied, heldMem, movedMem string

	s.Step("D-854/a-rule-and-a-memory-in-two-folders", func() {
		ada.Lore(pc.Dir, "store", "init", "--name", "me", "--url", notes.SSHURL(), "--yes").OK(t)
		for _, f := range []string{"a", "b"} {
			ada.Lore(pc.Dir, "store", "folder", "add", "--store", "me", "--folder", f).OK(t)
		}
		curated = oAddedID(t, ada.Lore(pc.Dir, "rule", "add", "--to", "me/b", "--body", "Use portless for dev URLs.", "--json").OK(t))
		copied = oAddedID(t, ada.Lore(pc.Dir, "rule", "add", "--to", "me/a", "--body", "use portless for dev urls\nso every dev server has a stable name", "--json").OK(t))
		heldMem = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--to", "me/b", "--body", "Invoices round at the line, never the total.", "--json").OK(t))
		movedMem = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--to", "me/a", "--body", "invoices round at the line, never the total", "--json").OK(t))
		if curated == "" || copied == "" || heldMem == "" || movedMem == "" {
			t.Fatal("an add --json printed no id")
		}
	})

	s.Step("D-854/move-id-lists-the-pair-both-kept", func() {
		compare := "compare: lore show --id " + copied + " --store me/b && lore show --id " + curated + " --store me/b"
		drop := "lore rule delete --id " + copied + " --yes"
		ada.Lore(pc.Dir, "move", "--id", copied, "--to", "me/b", "--yes").OK(t).
			Says(t, "moved to store me/b", "1 near-duplicate", copied+" ~ "+curated, "(same title)", "both kept", compare, drop)
		// each printed read runs where printed
		ada.Lore(pc.Dir, "show", "--id", copied, "--store", "me/b").OK(t)
		ada.Lore(pc.Dir, "show", "--id", curated, "--store", "me/b").OK(t)
	})

	s.Step("D-854/memory-move-lists-the-pair", func() {
		out := ada.Lore(pc.Dir, "memory", "move", "--id", movedMem, "--to", "me/b", "--yes").OK(t).
			Says(t, "1 near-duplicate", movedMem+" ~ "+heldMem, "lore memory delete --id "+heldMem+" --yes").Stdout
		if !strings.Contains(out, "--store me/b") {
			t.Errorf("memory move's compare does not read the folder:\n%s", out)
		}
		ada.Lore(pc.Dir, "show", "--id", heldMem, "--store", "me/b").OK(t)
	})
}
