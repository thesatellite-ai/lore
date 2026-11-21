//go:build journeys && !windows

package journeys

// relative_dates_test.go — bug hunt H-63 (devco D-801): a person asks
// "what happened 2 days ago", "since last week", "2 fridays ago". Every
// one of those was E_DATE ("not a date lore reads"); only yesterday,
// weekdays and "in 3d" read. Now the past phrases read on every date
// flag, and a span (last week) as a --since/--until pair keeps all of it.
// The clock is the journey's (Mon 2026-01-05 09:00 UTC).

import "testing"

func TestJourneyRelativeDates(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ivy := w.Persona("Ivy Notes", "ivy@example.test")
	app := ivy.Laptop().Clone(w.Remote("github.com/ivy/orbit"), "orbit")

	w.Step("day1/past-phrases-read-on-at", func() {
		app.Lore("init", "--non-interactive", "--name=orbit").OK(t)
		app.Lore("memory", "add", "--body", "orbit kickoff call held", "--at", "2 fridays ago").OK(t).Says(t, "2025-12-26", `from "2 fridays ago"`)
		app.Lore("memory", "add", "--body", "orbit sunday prep notes", "--at", "8 days ago").OK(t).Says(t, "2025-12-28")
		app.Lore("memory", "add", "--body", "orbit vendor quoted forty units", "--at", "last fri").OK(t).Says(t, "2026-01-02")
		app.Lore("memory", "add", "--body", "orbit budget approved", "--at", "2 days ago").OK(t).Says(t, "2026-01-03")
	})

	w.Step("day1/last-week-is-the-whole-week", func() {
		app.Lore("memory", "list", "--since", "last week", "--until", "last week").OK(t).
			Says(t, "orbit vendor quoted forty units", "orbit budget approved").
			Never(t, "orbit kickoff call held", "orbit sunday prep notes", "E_DATE")
		app.Lore("memory", "list", "--since", "2 days ago").OK(t).Says(t, "orbit budget approved").Never(t, "orbit vendor quoted", "E_DATE")
		app.Lore("search", "orbit", "--since", "2 fridays ago", "--until", "last fri").OK(t).
			Says(t, "orbit kickoff call held", "orbit vendor quoted forty units").Never(t, "orbit budget approved", "E_DATE")
	})

	w.Step("day1/duration-flags-read-phrases-too", func() {
		app.Lore("trash", "list", "--since", "2 days ago").OK(t).Never(t, "E_INVALID_INPUT")
		app.Lore("batch", "list", "--since", "last week").OK(t).Never(t, "E_INVALID_INPUT")
		app.Lore("memory", "list", "--since", "next week").Fails(t, 20, "E_DATE", "2 days ago")
	})
}
