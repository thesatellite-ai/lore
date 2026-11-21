//go:build journeys && !windows

package journeys

// adopt_day_search_test.go — devco:D-796 (owner P-13): a journal adopted
// into a folder of the personal store keeps its day metadata, yet
// `lore search --query "day:2026-11-18" --store me` printed "(no matches)":
// a metadata condition read the repo's rows only, never a store's. Now the
// condition finds the store's rows, labelled with their folder, as the
// same words do.

import (
	"testing"
)

func TestJourneyAdoptedDayMetadataIsSearchable(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	me := w.Remote("github.com/nia-okoro/lore-me")
	old := lap.Clone(w.Remote("github.com/nia-okoro/old-journal"), "old-journal")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")

	w.Step("day1/an-old-journal-with-day-metadata", func() {
		lap.LoreIn(old, "init").OK(t)
		lap.LoreIn(old, "memory", "add", "--body", "Met alice-ng about the orbit launch", "--meta", "day=2026-11-18").OK(t)
		lap.LoreIn(old, "memory", "add", "--body", "Quiet day, read the orbit notes", "--meta", "day=2026-11-17").OK(t)
		old.CommitAll("journal")
	})

	w.Step("day1/adopt-it-into-a-personal-store-folder", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "journal").OK(t)
		lap.LoreIn(old, "store", "adopt", "--target", "me/journal", "--yes").OK(t)
	})

	w.Step("day1/the-day-finds-the-adopted-row", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "search", "--query", "day:2026-11-18", "--store", "me").OK(t).
			Says(t, "Met alice-ng about the orbit launch", "(day: 2026-11-18)", "[me/journal]").
			Never(t, "(no matches)", "Quiet day")
	})
}
