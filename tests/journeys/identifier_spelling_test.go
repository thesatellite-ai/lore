//go:build journeys && !windows

package journeys

// identifier_spelling_test.go — devco:D-1009 (bug hunt H-271): a person
// searching for a config key the way the config file writes it
// (enabled_jobs) got "(no matches)" when the note named it the way the
// code does (enabledJobs): the index reads enabledJobs as one word and
// enabled_jobs as two. Now a name written with _ - or . is also searched
// with its parts written together, so either spelling finds the note, in
// lore search and in lore ask alike.

import (
	"testing"
)

func TestJourneyIdentifierSpellingFindsTheNote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")

	w.Step("day1/a-note-names-the-key-in-camel-case", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "The cron binary starts every job named in its enabledJobs list; an empty list starts them all.").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "Webhook replies go to the callbackHost each app sets.").OK(t)
	})

	w.Step("day1/the-config-spelling-finds-it", func() {
		lap.LoreIn(code, "search", "--query", "enabled_jobs").OK(t).
			Says(t, "enabledJobs list").
			Never(t, "(no matches)")
		lap.LoreIn(code, "search", "--query", "callback-host").OK(t).
			Says(t, "callbackHost each app sets").
			Never(t, "(no matches)")
		lap.LoreIn(code, "ask", "--query", "what starts when enabled_jobs is empty").OK(t).
			Says(t, "enabledJobs list")
	})
}
