//go:build journeys && !windows

package journeys

// kind_search_question_test.go — a teammate asks one kind's own search a
// question (devco:D-461, D-468): `lore decision search "how do we deploy
// orbit"` finds the deploy decision as `lore search` does (before, every
// word, "how" and "we" too, had to match: "(no matches)"), and a prefix
// query (invoi*) lists the decision titled about invoices first.

import (
	"strings"
	"testing"
)

func TestJourneyKindSearchQuestion(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kai := w.Persona("Kai Moreno", "kai@larkfield.example")
	lap := kai.Laptop()
	repo := lap.Clone(w.Remote("github.com/larkfield/orbit"), "orbit")

	w.Step("day1/notes", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "decision", "add", "--title", "Quarterly report", "--body", "Attach the invoices: invoices, invoicing, invoice totals and every invoice number.").OK(t)
		lap.LoreIn(repo, "decision", "add", "--title", "Orbit deploys with lath", "--body", "Run lath run deploy staging to ship orbit; production needs a release tag.").OK(t)
		lap.LoreIn(repo, "decision", "add", "--title", "Invoice numbering", "--body", "Numbers are sequential per year.").OK(t)
		lap.LoreIn(repo, "rule", "add", "--body", "Run task dev to start orbit locally; it reads .env.local.").OK(t)
	})

	w.Step("day1/kind-search-reads-a-question", func() {
		out := lap.LoreIn(repo, "decision", "search", "--query", "how do we deploy orbit").OK(t).Never(t, "(no matches)").Stdout
		if first, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); !strings.Contains(first, "Orbit deploys with lath") {
			t.Errorf("first decision hit %q: want the deploy decision\n%s", first, out)
		}
		lap.LoreIn(repo, "rule", "search", "--query", "how do I run orbit locally").OK(t).Says(t, "Run task dev to start orbit locally")
	})

	w.Step("day1/prefix-ranks-the-title-first", func() {
		out := lap.LoreIn(repo, "decision", "search", "--query", "invoi*").OK(t).Stdout
		if first, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); !strings.Contains(first, "Invoice numbering") {
			t.Errorf("first hit %q: want the decision titled about invoices\n%s", first, out)
		}
	})
}
