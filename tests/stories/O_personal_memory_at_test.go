//go:build stories && !windows

package stories

// O_personal_memory_at_test.go — story O, devco D-765 (owner P-9, bug hunt
// H-24): a note written today about last week's call could not carry the
// day it is about. `memory add` had no flag for valid_at, and no list or
// search could ask for the notes about a given day, so on a personal
// knowledge base a question like "what did the vendor quote in March"
// took a sql query by hand. Here one owner-shaped laptop keeps a personal
// store on a local bare repo, adds a memory with --at and one without from
// home, and memory list, memory search, lore search and lore sql each
// answer the March question in one call.

import (
	"strings"
	"testing"
)

// TestStoryOPersonalMemoryAt: memory add --at sets valid_at; --since /
// --until on memory list, memory search and lore search filter by it
// (devco D-765).
func TestStoryOPersonalMemoryAt(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})

	to := "me/notes"
	s.Step("D-765/memory-add-at-sets-valid-at", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "notes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "call with the printer vendor: quote 40 units", "--at", "2025-03-14", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "kickoff of the vendor garden project", "--to", to, "--yes").OK(t)
		out := ada.Lore(ada.Home, "sql", "--format", "csv", "--query", "SELECT substr(valid_at,1,10) AS d FROM memories WHERE body LIKE 'call with%'", "--store", to).OK(t).Out()
		if !strings.Contains(out, "2025-03-14") {
			t.Errorf("[O D-765] memory add --at 2025-03-14 stored no valid_at of that day:\n%s", out)
		}
	})

	s.Step("D-765/one-call-answers-the-march-question", func() {
		march := []string{"--since", "2025-03-01", "--until", "2025-03-31", "--store", to}
		for _, args := range [][]string{
			append([]string{"memory", "search", "--query", "vendor"}, march...),
			append([]string{"memory", "list"}, march...),
			append([]string{"search", "--query", "vendor"}, march...),
		} {
			out := ada.Lore(ada.Home, args...).OK(t).Out()
			if !strings.Contains(out, "printer vendor") || strings.Contains(out, "garden") {
				t.Errorf("[O D-765] `lore %s` must list only the memory about March:\n%s", strings.Join(args, " "), out)
			}
		}
		// a memory without --at is dated by when it was added
		out := ada.Lore(ada.Home, "memory", "list", "--since", "today", "--store", to).OK(t).Out()
		if !strings.Contains(out, "garden") || strings.Contains(out, "printer vendor") {
			t.Errorf("[O D-765] memory list --since today must list the memory added today only:\n%s", out)
		}
	})
}
