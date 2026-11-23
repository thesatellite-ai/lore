//go:build stories && !windows

package stories

// O_searchindex_files_test.go — story O, devco D-920 (hunt H-178): on the
// owner-shaped laptop a memory is edited on a work branch and committed
// there; switching back to main puts the older row file in place. Search
// and ask read the cache, which used to keep the branch's newer copy (it
// never took an older version back, and its cursor moved past the file):
// they answered with the branch's text while `lore show --id` showed
// main's file. Now every read follows the files on each branch, and
// doctor's search_index check stays quiet.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOSearchFollowsTheBranchFiles(t *testing.T) {
	s := New(t, "O")
	ledger := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/ledger-app"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	lc := ada.CloneTo(ledger, filepath.Join(ada.Home, "src", "ledger-app"))
	var id string

	s.Step("D-920/memory-on-main", func() {
		ada.Lore(lc.Dir, "init", "--non-interactive", "--name=ledger-app").OK(t)
		r := ada.Lore(lc.Dir, "memory", "add", "--body", "The invoice queue drains every ten minutes.", "--to", "repo", "--json").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		lc.CommitAll("lore: invoice note")
	})

	s.Step("D-920/edited-on-a-branch", func() {
		lc.Git("switch", "-q", "-c", "wip")
		ada.Lore(lc.Dir, "memory", "edit", "--id", id, "--body", "The invoice queue drains every hourly batch.").OK(t)
		lc.CommitAll("lore: invoice note on wip")
		ada.Lore(lc.Dir, "search", "--query", "hourly").OK(t).Says(t, id)
	})

	s.Step("D-920/back-on-main-every-read-follows-the-file", func() {
		lc.Git("switch", "-q", "main")
		ada.Lore(lc.Dir, "show", "--id", id).OK(t).Says(t, "every ten minutes").Never(t, "hourly")
		ada.Lore(lc.Dir, "search", "--query", "invoice").OK(t).Says(t, id, "every ten minutes").Never(t, "hourly")
		ada.Lore(lc.Dir, "search", "--query", "hourly").OK(t).Never(t, id)
		ada.Lore(lc.Dir, "ask", "--query", "how often does the invoice queue drain").OK(t).Says(t, "every ten minutes").Never(t, "hourly")
		ada.Lore(lc.Dir, "doctor").Never(t, "search_index")
	})

	s.Step("D-920/back-on-the-branch", func() {
		lc.Git("switch", "-q", "wip")
		ada.Lore(lc.Dir, "search", "--query", "invoice").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
	})
}
