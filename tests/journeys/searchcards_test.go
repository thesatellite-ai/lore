//go:build journeys && !windows

package journeys

// searchcards_test.go — a lead asks search about an admin page and a
// team whose handle holds "admins" and whose display name holds the
// question's common words came first (devco:D-1007, set 8 ask 3). A
// card's description words now count by their rarity, so the knowledge
// row about the page leads; searching the team's handle still finds the
// team first.
//
//	team @ops-admins "Ops admins (company store owners)" + a note on the admin page
//	  ─▶ search "which admin page shows the company store" ─▶ the note first
//	  ─▶ search "ops admins" ─▶ the team first

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// jsrcFolder is the store folder of the search-cards journey.
const jsrcFolder = "tenants"

func TestJourneySearchCardsNeverLeadOnCommonWords(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	var folder string

	w.Step("day1/a-team-and-notes", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jsrcFolder).OK(t)
		lee.Lore(parent, "team", "add", "--handle", "@ops-admins", "--name", "Ops admins (company store owners)", "--members", "@lee", "--yes").OK(t)
		out := lee.Lore(parent, "memory", "add", "The admin page lists every tenant with its plan and seats", "--to", tlOrg+"/"+jsrcFolder, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(mem_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no memory id in %s", out)
		}
		// the common words: several rows hold one of them
		for _, b := range []string{"The company store sells stickers", "Store hours are nine to five", "Company retreat is in May", "The store keeps receipts for a year", "Company laptops are refreshed every three years", "Store returns need a receipt", "Company holidays follow the calendar", "Store credit never expires"} {
			lee.Lore(parent, "memory", "add", b, "--to", tlOrg+"/"+jsrcFolder).OK(t)
		}
		folder = filepath.Dir(filepath.Dir(jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t)).Path))
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day1/the-note-leads-the-team-follows", func() {
		out := lee.Lore(folder, "search", "which admin page shows the company store").OK(t).Out()
		note, team := strings.Index(out, "The admin page lists"), strings.Index(out, "@ops-admins")
		if note < 0 || (team >= 0 && team < note) {
			t.Fatalf("want the admin page note before the team:\n%s", out)
		}
	})

	w.Step("day1/the-handle-finds-the-team-first", func() {
		out := lee.Lore(folder, "search", "ops admins").OK(t).Out()
		first := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "score=") {
				first = l
				break
			}
		}
		if !strings.Contains(first, "@ops-admins") {
			t.Fatalf("want the team first for its handle, first hit %q:\n%s", first, out)
		}
	})
}
