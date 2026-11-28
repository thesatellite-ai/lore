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

// TestJourneySearchCardsNeverLeadPunctuatedQuestions (devco:D-1034, bug
// hunt H-299): a lead pastes a long question with a parenthesis, an
// apostrophe and a question mark into search. It read as FTS5 syntax, the
// people and teams were ranked as if they held every word (each held one:
// the company name in every email), and the first results were all cards;
// the apostrophe made the rest of the question one phrase no note held.
// Now the note that answers it leads and no card is in the first results;
// a question naming a team or a person still finds that card first.
func TestJourneySearchCardsNeverLeadPunctuatedQuestions(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	const answer = "The billing page rounds invoice totals to the cent before tax; the nightly export retries a failed upload three times"
	var folder string

	tt.w.Step("day1/ten-people-two-teams-and-notes", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jsrcFolder).OK(t)
		for _, n := range []string{"Avery Cole", "Blake Diaz", "Casey Ford", "Devon Gray", "Emery Hale", "Finley Ives", "Harper Jones", "Jordan Kent", "Morgan Lane", "Quinn Moss"} {
			first := strings.ToLower(strings.Fields(n)[0])
			lee.Lore(parent, "member", "add", "--email", first+"@acme-billing.example", "--name", n, "--yes").OK(t)
		}
		lee.Lore(parent, "team", "add", "--handle", "@billing", "--name", "Billing (acme invoices)", "--members", "@lee", "--yes").OK(t)
		lee.Lore(parent, "team", "add", "--handle", "@export", "--name", "Export (nightly upload)", "--members", "@lee", "--yes").OK(t)
		out := lee.Lore(parent, "memory", "add", answer, "--to", tlOrg+"/"+jsrcFolder, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(mem_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no memory id in %s", out)
		}
		for _, b := range []string{"Acme invoices are sent on the first of the month", "The export job writes a CSV for finance", "Tax rates live in the billing settings", "Uploads go to the shared bucket", "Totals on the dashboard refresh hourly"} {
			lee.Lore(parent, "memory", "add", b, "--to", tlOrg+"/"+jsrcFolder).OK(t)
		}
		folder = filepath.Dir(filepath.Dir(jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t)).Path))
		lee.Lore(parent, "sync").OK(t)
	})

	hits := func(out string) []string {
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "score=") {
				lines = append(lines, l)
			}
		}
		return lines
	}
	card := func(l string) bool { return strings.HasPrefix(l, "member ") || strings.HasPrefix(l, "team ") }

	tt.w.Step("day1/the-note-leads-a-punctuated-question", func() {
		for _, q := range []string{
			"How does acme's billing page (the invoice view) round totals, and how often does the nightly export retry a failed upload?",
			"Billing page (acme-billing): when are totals rounded, before or after tax?",
		} {
			out := lee.Lore(folder, "search", "--query", q).OK(t).Out()
			lines := hits(out)
			if len(lines) == 0 || !strings.Contains(out, "The billing page rounds") || card(lines[0]) {
				t.Fatalf("%q: want the billing note first:\n%s", q, out)
			}
			for _, l := range lines[:min(3, len(lines))] {
				if card(l) {
					t.Fatalf("%q: a person or team is among the first results:\n%s", q, out)
				}
			}
		}
	})

	tt.w.Step("day1/a-team-or-person-question-still-finds-the-card", func() {
		for _, c := range []struct{ q, want string }{
			{"who owns billing?", "@billing"},
			{"Quinn Moss", "Quinn Moss"},
			{"export (team)", "@export"},
		} {
			out := lee.Lore(folder, "search", "--query", c.q).OK(t).Out()
			if !strings.Contains(out, c.want) {
				t.Fatalf("%q: want %s among the results:\n%s", c.q, c.want, out)
			}
		}
		lines := hits(lee.Lore(folder, "search", "--query", "Quinn Moss").OK(t).Out())
		if len(lines) == 0 || !strings.Contains(lines[0], "Quinn Moss") {
			t.Fatalf("a person's name: want the person first, got %q", lines)
		}
	})
}
