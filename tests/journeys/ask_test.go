//go:build journeys && !windows

package journeys

// ask_test.go — devco:D-807 (gate G-3, bug hunt H-58): a person keeps
// people and projects as resources in their personal store and asks lore
// questions in their own words from a code repo; each question took a
// different command, and a typo in a name or a decision asked in other
// words found nothing. Now one `lore ask --query` answers each.

import (
	"testing"
)

func TestJourneyAskAnswersInOneCall(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	me := w.Remote("github.com/nia-okoro/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")

	w.Step("day1/people-and-notes-in-the-personal-store", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		lap.Lore(lap.Home, "resource", "kind", "add", "--kind", "person", "--name-format", "dns", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "resource", "field", "add", "--kind", "person", "--field", "role", "--type", "text", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "resource", "add", "--kind", "person", "--name", "keiko-sato", "--set", "role=lawyer", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "memory", "add", "--body", "Decision for orbit: we chose hetzner over aws because the bill is 30 percent smaller.", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "task", "add", "--title", "send the contract to keiko-sato", "--meta", "owed_by=me", "--meta", "owed_to=keiko-sato", "--to", "me/work", "--yes").OK(t)
	})

	w.Step("day1/ask-from-the-code-repo", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "ask", "--store", "me", "--query", "who is keiko-satto").OK(t).
			Says(t, "person keiko-sato", "role: lawyer", "cited: res_")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "who is our lawyer").OK(t).
			Says(t, "person keiko-sato")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "what do I owe keiko-sato").OK(t).
			Says(t, "send the contract to keiko-sato", "owed by me to keiko-sato")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "what was the reasoning behind orbit's pick").OK(t).
			Says(t, "we chose hetzner over aws because the bill is 30 percent smaller")
	})
}
