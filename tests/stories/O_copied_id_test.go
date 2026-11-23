//go:build stories && !windows

package stories

// O_copied_id_test.go — story O, devco D-929 (hunt H-187, a leftover of
// H-178 / #142): on the owner-shaped laptop the repo's notes are copied
// into a store folder the repo attaches with `lore store adopt` (ids kept), then a
// memory is edited. The copy the edit did not reach kept its old text,
// and every list read showed both: a search for a word only the old text
// holds still found it, and `lore ask` answered from the nearest store's
// copy. Now list, search and ask show the newest copy only, and the old
// words find nothing.

import (
	"strings"
	"testing"
)

func TestStoryOCopiedIDShowsTheNewestCopy(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oOwner+"/billing-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	bc := ada.CloneTo(billing, ada.OrgDir("billing-app"))
	notes := s.Remote("github.com/" + oOwner + "/notes")
	const folder = "invoices"
	var id string

	s.Step("D-929/a-memory-copied-into-a-store-folder", func() {
		ada.Lore(bc.Dir, "init", "--non-interactive", "--name=billing-app").OK(t)
		ada.Lore(bc.Dir, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		ada.Lore(bc.Dir, "store", "folder", "add", "--store", "notes", "--folder", folder).OK(t)
		r := ada.Lore(bc.Dir, "memory", "add", "--body", "The invoice queue drains every ten minutes.", "--to", "repo", "--json").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		bc.CommitAll("lore: invoice note")
		ada.Lore(bc.Dir, "search", "--query", "invoice").OK(t).Says(t, id) // the read server starts
		ada.Lore(bc.Dir, "store", "adopt", "--target", "notes/"+folder, "--yes").OK(t)
	})

	s.Step("D-929/edited-then-every-reader-shows-the-edit", func() {
		ada.Lore(bc.Dir, "memory", "edit", "--id", id, "--body", "The invoice queue drains every hourly batch.").OK(t)
		ada.Lore(bc.Dir, "search", "--query", "invoice").OK(t).Says(t, id, "every hourly batch").Never(t, "ten minutes")
		r := ada.Lore(bc.Dir, "search", "--query", "minutes").OK(t)
		oCheck(t, "D-929/search-old-words", !strings.Contains(r.Out(), id),
			"a word only the stale copy holds still finds it:\n%s", r.Out())
		ada.Lore(bc.Dir, "memory", "list").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
		ada.Lore(bc.Dir, "ask", "--query", "how often does the invoice queue drain").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
	})
}
