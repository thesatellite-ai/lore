//go:build journeys && !windows

package journeys

// search_punct_test.go — a person searches the way they write notes
// (devco D-798, P-14): `lore search --query "alice (person)"` failed with
// "fts5: syntax error near ..." because the parenthesis was read as search
// syntax. Punctuation around words is now read as the words, in `lore
// search` (with or without a metadata condition) and in every `lore <kind>
// search`; the documented syntax still works, and a query with no word
// at all is still refused with its hint. A colon inside a word separates
// words (devco D-964, H-223): `lint:no-os-getenv` finds the task about the
// no-os-getenv lint.

import "testing"

func TestJourneySearchPunctuation(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ana := w.Persona("Ana Reyes", "ana@larkfield.example")
	lap := ana.Laptop()
	repo := lap.Clone(w.Remote("github.com/larkfield/orbit"), "orbit")

	w.Step("day1/notes", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "alice-ng (person) leads orbit at acme-co", "--meta", "team=ops").OK(t)
		lap.LoreIn(repo, "decision", "add", "--title", "orbit (project) ships weekly", "--body", "alice-ng decided after the pilot").OK(t)
	})

	w.Step("day1/punctuation-reads-as-words", func() {
		for _, q := range []string{"alice (person)", "orbit)", "(acme", `"alice`, "alice^", "orbit AND"} {
			lap.LoreIn(repo, "search", "--query", q).OK(t).Says(t, "alice-ng (person) leads orbit").Never(t, "syntax error")
		}
		lap.LoreIn(repo, "search", "--query", "team:ops alice (person)").OK(t).Says(t, "alice-ng (person) leads orbit").Never(t, "syntax error")
		lap.LoreIn(repo, "memory", "search", "--query", "alice (person)").OK(t).Says(t, "alice-ng (person) leads orbit")
		lap.LoreIn(repo, "decision", "search", "--query", "orbit (project)").OK(t).Says(t, "orbit (project) ships weekly")
	})

	w.Step("day1/a-colon-separates-words", func() {
		// devco D-964 (H-223): task names carry colons; lint:no-os-getenv
		// found nothing as one phrase, now it finds the row saying the words
		lap.LoreIn(repo, "task", "add", "--title", "Drop the os.Getenv calls flagged by the no-os-getenv lint").OK(t)
		for _, args := range [][]string{{"search"}, {"task", "search"}} {
			lap.LoreIn(repo, append(args, "--query", "lint:no-os-getenv")...).OK(t).Says(t, "flagged by the no-os-getenv lint").Never(t, "(no matches)")
		}
	})

	w.Step("day1/syntax-still-works", func() {
		lap.LoreIn(repo, "search", "--query", `"ships weekly"`).OK(t).Says(t, "orbit (project) ships weekly").Never(t, "alice-ng (person) leads")
		lap.LoreIn(repo, "search", "--query", "pilo*").OK(t).Says(t, "orbit (project) ships weekly")
		lap.LoreIn(repo, "search", "--query", "(").Refused(t, "E_INVALID_INPUT", "not a valid search query")
	})
}
