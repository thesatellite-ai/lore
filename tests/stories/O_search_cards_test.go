//go:build stories && !windows

package stories

// O_search_cards_test.go — story O, devco:D-1034 (bug hunt H-299, a
// benchmark team's set 10 ask e): their questions are long and carry a
// parenthesis, an apostrophe and a question mark, and `lore search` gave
// back only people and teams. Read as FTS5 syntax, every person or team
// holding one word of the question (the company name in every email, a
// team handle that is also the product's name) ranked as if it held all
// of them; the apostrophe made the rest of the question one phrase no
// note held. Here the owner-shaped admin keeps notes in an org repo, adds
// ten people and two teams to the roster and asks the way that team does:
// the note that answers leads and no card is among the first results; a
// question about who owns something and a person's name still find the
// card.

import (
	"strings"
	"testing"
)

// TestStoryOSearchCards: in a long question cards follow the knowledge
// rows; a question about people or a name still finds them (devco:D-1034).
func TestStoryOSearchCards(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))

	const answer = "The billing page rounds invoice totals to the cent before tax; the nightly export retries a failed upload three times"
	s.Step("D-1034/a-roster-of-ten-and-notes", func() {
		oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
			map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
		for _, n := range []string{"Avery Cole", "Blake Diaz", "Casey Ford", "Devon Gray", "Emery Hale", "Finley Ives", "Harper Jones", "Jordan Kent", "Morgan Lane", "Quinn Moss"} {
			first := strings.ToLower(strings.Fields(n)[0])
			ada.LoreIn(pc, "member", "add", "--email", first+"@acme.example", "--name", n, "--yes").OK(t)
		}
		ada.LoreIn(pc, "team", "add", "--handle", "@billing", "--name", "Billing (acme invoices)", "--members", oAdminOrg.Email, "--yes").OK(t)
		ada.LoreIn(pc, "team", "add", "--handle", "@export", "--name", "Export (nightly upload)", "--members", oAdminOrg.Email, "--yes").OK(t)
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		ada.LoreIn(pc, "memory", "add", "--body", answer).OK(t)
		for _, b := range []string{"Acme invoices are sent on the first of the month", "The export job writes a CSV for finance", "Tax rates live in the billing settings", "Uploads go to the shared bucket", "Totals on the dashboard refresh hourly"} {
			ada.LoreIn(pc, "memory", "add", "--body", b).OK(t)
		}
		ada.LoreIn(pc, "sync").OK(t)
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

	s.Step("D-1034/a-long-question-leads-with-the-note", func() {
		for _, c := range []struct{ cell, q string }{
			{"D-1034/search-punctuated-question", "How does acme's billing page (the invoice view) round totals, and how often does the nightly export retry a failed upload?"},
			{"D-1034/search-plain-long-question", "how does the acme billing page round invoice totals before tax"},
		} {
			r := ada.LoreIn(pc, "search", "--query", c.q)
			lines := hits(r.Out())
			ok := r.Code == 0 && len(lines) > 0 && strings.Contains(lines[0], "The billing page rounds")
			for _, l := range lines[:min(3, len(lines))] {
				ok = ok && !card(l)
			}
			oCheck(t, c.cell, ok, "`lore search --query %q`: want the billing note first and no person or team in the first three:\n%s", c.q, r)
		}
	})

	s.Step("D-1034/a-question-about-people-finds-the-card", func() {
		for _, c := range []struct{ cell, q, want string }{
			{"D-1034/search-who-owns", "who owns billing?", "@billing"},
			{"D-1034/search-which-team-long", "which team runs the nightly export of acme invoices?", "@export"},
			{"D-1034/search-person-name", "Quinn Moss", "Quinn Moss"},
		} {
			r := ada.LoreIn(pc, "search", "--query", c.q)
			oCheck(t, c.cell, r.Code == 0 && strings.Contains(r.Out(), c.want), "`lore search --query %q`: want %s among the results:\n%s", c.q, c.want, r)
		}
		lines := hits(ada.LoreIn(pc, "search", "--query", "Quinn Moss").OK(t).Out())
		oCheck(t, "D-1034/search-person-name-first", len(lines) > 0 && strings.Contains(lines[0], "Quinn Moss"), "a person's name: want the person first, got %q", lines)
	})
}
