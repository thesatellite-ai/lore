//go:build journeys && !windows

package journeys

// ask_test.go — devco:D-807 (gate G-3, bug hunt H-58): a person keeps
// people and projects as resources in their personal store and asks lore
// questions in their own words from a code repo; each question took a
// different command, and a typo in a name or a decision asked in other
// words found nothing. Now one `lore ask --query` answers each.
//
// devco:D-818 (bug hunt H-78): the named project printed first and its
// status update second, the person before what you owe them; now the item
// that answers the question comes first and the resource follows it.

import (
	"strings"
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
		lap.Lore(lap.Home, "resource", "kind", "add", "--kind", "project", "--name-format", "dns", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "resource", "add", "--kind", "project", "--name", "orbit", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "memory", "add", "--body", "Status update orbit: live in production with 9 customers.", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "task", "add", "--title", "send the contract to keiko-sato", "--meta", "owed_by=me", "--meta", "owed_to=keiko-sato", "--to", "me/work", "--yes").OK(t)
	})

	w.Step("day1/ask-from-the-code-repo", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "ask", "--store", "me", "--query", "who is keiko-satto").OK(t).
			Says(t, "keiko-sato (person", "role: lawyer")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "who is our lawyer").OK(t).
			Says(t, "keiko-sato (person")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "what do I owe keiko-sato").OK(t).
			Says(t, "send the contract to keiko-sato\ncited: tsk_")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "what was the reasoning behind orbit's pick").OK(t).
			Says(t, "we chose hetzner over aws because the bill is 30 percent smaller")
		status := lap.LoreIn(code, "ask", "--store", "me", "--query", "what is the status of orbit").OK(t).
			Says(t, "live in production with 9 customers", " (project) orbit").Out() // H-245: a candidate line
		if strings.Index(status, "live in production") > strings.Index(status, " (project) orbit") {
			t.Errorf("the project is printed before its status update:\n%s", status)
		}
		// the task line names the person: no card of them under it (devco D-827)
		owe := lap.LoreIn(code, "ask", "--store", "me", "--query", "what do I owe keiko-sato").OK(t).Out()
		if strings.Contains(owe, "keiko-sato (person") {
			t.Errorf("the debt answer repeats the person under the task that names them:\n%s", owe)
		}
	})

	// devco:D-828 (bug hunt H-86): from the home folder ask with no --store
	// refused E_NOT_A_STORE; it is a read, so it reads the personal store
	// there and says so.
	w.Step("day1/ask-from-the-home-folder", func() {
		lap.Lore(lap.Home, "ask", "--query", "what is the status of orbit").OK(t).
			Says(t, "live in production with 9 customers", "note: read your personal store (not in a repo)")
	})
}
