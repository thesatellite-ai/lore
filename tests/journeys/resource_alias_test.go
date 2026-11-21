//go:build journeys && !windows

package journeys

// resource_alias_test.go — devco:D-817 (bug hunt H-75, owner HIGH): a
// person keeps colleagues as resources; their nicknames, full names and
// roles were only field values, so nothing but a field-value match in
// `lore ask` knew them. Now `lore resource alias add` gives a resource
// other names, and show, ${res:…}, search and ask (also a typo of one)
// find the resource by them, from a code repo as from home.

import (
	"testing"
)

func TestJourneyResourceAliasesFindThePerson(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	me := w.Remote("github.com/nia-okoro/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")

	w.Step("day1/a-colleague-with-other-names", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		lap.Lore(lap.Home, "resource", "kind", "add", "--kind", "person", "--name-format", "dns", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "resource", "add", "--kind", "person", "--name", "keiko-sato", "--to", "me/work", "--yes").OK(t)
		lap.Lore(lap.Home, "resource", "alias", "add", "--store", "me/work", "--id", "keiko-sato", "--alias", "Kiki", "--alias", "general counsel").OK(t).
			Says(t, "alias Kiki, general counsel for person/keiko-sato")
		lap.Lore(lap.Home, "memory", "add", "--body", "Ask ${res:Kiki} about the contract.", "--to", "me/work", "--yes").OK(t)
	})

	w.Step("day1/find-her-by-any-name-from-the-code-repo", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "resource", "show", "--store", "me/work", "--id", "kiki").OK(t).
			Says(t, "person/keiko-sato", "aliases: Kiki, general counsel")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "everything about our general counsel").OK(t).
			Says(t, "person keiko-sato", "Ask ${res:Kiki} about the contract.")
		lap.LoreIn(code, "ask", "--store", "me", "--query", "what about kikki").OK(t).
			Says(t, "person keiko-sato")
		lap.LoreIn(code, "search", "--store", "me", "--query", "counsel").OK(t).
			Says(t, "keiko-sato")
		lap.LoreIn(code, "resource", "alias", "list", "--store", "me/work").OK(t).
			Says(t, "person/keiko-sato  Kiki, general counsel")
	})
}
