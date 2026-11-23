//go:build stories && !windows

package stories

// O_searchindex_rename_test.go — story O, devco D-930 (hunt H-188, public
// issue #142): on the owner-shaped laptop a store folder is attached as
// kb/app, a memory is written there, then the folder is attached under
// another name (notes) and the memory edited. A folder's cache kept each
// row under the name it was read under, so the copy under kb/app stayed:
// search and ask kept finding the memory by its old text until a
// `lore search rebuild`. Now the next command drops the rows under the
// name no attachment uses, and doctor's search_index check stays quiet.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOSearchAfterTheFolderIsAttachedUnderANewName(t *testing.T) {
	s := New(t, "O")
	ledger := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/ledger-app"), func(dir string) { PlainRepo(t, dir) })
	kb := s.Remote("github.com/" + oAdminPersonal.Login + "/kb")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	lc := ada.CloneTo(ledger, filepath.Join(ada.Home, "src", "ledger-app"))
	var id string

	s.Step("D-930/memory-in-kb-app", func() {
		ada.Lore(lc.Dir, "init", "--non-interactive", "--name=ledger-app").OK(t)
		ada.Lore(lc.Dir, "store", "init", "--name", "kb", "--url", kb.SSHURL(), "--yes").OK(t)
		r := ada.Lore(lc.Dir, "memory", "add", "--to", "kb/app", "--create", "--body", "Pelican invoices are exported nightly to the archive bucket.", "--json", "--yes").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		ada.Lore(lc.Dir, "search", "--query", "pelican").OK(t).Says(t, id)
	})

	s.Step("D-930/attached-as-notes-then-edited", func() {
		ada.Lore(lc.Dir, "store", "remove", "--name", "kb/app", "--yes").OK(t)
		ada.Lore(lc.Dir, "store", "add", "--name", "notes", "--url", kb.SSHURL(), "--folder", "app", "--yes").OK(t)
		ada.Lore(lc.Dir, "memory", "edit", "--id", id, "--body", "Walrus receipts are exported hourly to the ledger service.", "--yes").OK(t)
	})

	s.Step("D-930/search-and-ask-show-only-the-new-text-without-a-rebuild", func() {
		ada.Lore(lc.Dir, "show", "--id", id).OK(t).Says(t, "Walrus receipts").Never(t, "Pelican")
		ada.Lore(lc.Dir, "search", "--query", "pelican invoices").OK(t).Never(t, id, "Pelican")
		ada.Lore(lc.Dir, "search", "--query", "walrus receipts").OK(t).Says(t, id, "Walrus receipts").Never(t, "Pelican")
		ada.Lore(lc.Dir, "ask", "--query", "how are pelican invoices exported?").OK(t).Never(t, "Pelican invoices are exported")
		ada.Lore(lc.Dir, "doctor").Never(t, "search_index")
	})
}

// TestStoryOOneFolderUnderTwoNamesInTwoCheckouts — story O, devco D-933
// (hunt H-194, public issue #142, the reporter's fix): two checkouts on the
// owner-shaped laptop attach ONE store folder under two names (kb/app in
// ledger-app, notes in billing-app). The folder's cache kept one copy per
// name, so after an edit in billing-app its search, which reads the whole
// shared file, still showed ledger-app's older copy. The cache now keys
// rows by the folder's identity: one copy, which the edit replaces.
func TestStoryOOneFolderUnderTwoNamesInTwoCheckouts(t *testing.T) {
	s := New(t, "O")
	ledger := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/ledger-app"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/billing-app"), func(dir string) { PlainRepo(t, dir) })
	kb := s.Remote("github.com/" + oAdminPersonal.Login + "/kb")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	lc := ada.CloneTo(ledger, filepath.Join(ada.Home, "src", "ledger-app"))
	bc := ada.CloneTo(billing, filepath.Join(ada.Home, "src", "billing-app"))
	var id string

	s.Step("D-933/memory-in-kb-app-from-ledger", func() {
		ada.Lore(lc.Dir, "init", "--non-interactive", "--name=ledger-app").OK(t)
		ada.Lore(lc.Dir, "store", "init", "--name", "kb", "--url", kb.SSHURL(), "--yes").OK(t)
		r := ada.Lore(lc.Dir, "memory", "add", "--to", "kb/app", "--create", "--body", "Pelican invoices are exported nightly to the archive bucket.", "--json", "--yes").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		ada.Lore(lc.Dir, "search", "--query", "pelican").OK(t).Says(t, id)
	})

	s.Step("D-933/billing-attaches-the-folder-as-notes-and-edits", func() {
		ada.Lore(bc.Dir, "init", "--non-interactive", "--name=billing-app").OK(t)
		ada.Lore(bc.Dir, "store", "add", "--name", "notes", "--url", kb.SSHURL(), "--folder", "app", "--yes").OK(t)
		ada.Lore(bc.Dir, "memory", "edit", "--id", id, "--body", "Walrus receipts are exported hourly to the ledger service.", "--yes").OK(t)
	})

	s.Step("D-933/both-checkouts-find-only-the-new-text", func() {
		ada.Lore(bc.Dir, "search", "--query", "pelican invoices").OK(t).Never(t, id, "Pelican")
		ada.Lore(bc.Dir, "ask", "--query", "how are pelican invoices exported?").OK(t).Never(t, "Pelican invoices are exported")
		ada.Lore(lc.Dir, "search", "--query", "pelican invoices").OK(t).Never(t, id, "Pelican")
		ada.Lore(lc.Dir, "search", "--query", "walrus receipts").OK(t).Says(t, id, "Walrus receipts", "[kb/app]").Never(t, "Pelican")
		ada.Lore(bc.Dir, "search", "--query", "walrus receipts").OK(t).Says(t, id, "Walrus receipts").Never(t, "Pelican")
		ada.Lore(bc.Dir, "doctor").Never(t, "search_index")
		ada.Lore(lc.Dir, "doctor").Never(t, "search_index")
	})
}
