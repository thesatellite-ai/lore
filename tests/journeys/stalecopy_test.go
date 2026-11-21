//go:build journeys && !windows

package journeys

// stalecopy_test.go — devco:D-853 (hunt H-110, owner org-setup item 10):
// a store's remote stops answering (wifi off, VPN down, the host moved).
// lore reads the store's local copy, as it should, but search, ask,
// memory search and an empty answer's searched: line showed those rows
// as the store's with no word that the copy may be a day old. Now each
// says "<store>: unreachable, results from the copy of <date>", --json
// lists it under "unreachable", doctor names the copy, and once a sync
// reaches the store again the line goes.

import (
	"testing"
)

func TestJourneyStaleCopySaysUnreachable(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ivo := w.Persona("Ivo Brant", "ivo@acme-co.example")
	lap := ivo.Laptop()
	notes := w.Remote("github.com/acme-co/lore-notes")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")
	const line = "notes: unreachable, results from the copy of "

	w.Step("day1/a-note-in-a-team-store", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "The vendor contract of orbit renews in march.", "--to", "notes", "--yes").OK(t)
		lap.LoreIn(code, "sync").OK(t)
		lap.LoreIn(code, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march").Never(t, "unreachable")
	})

	w.Step("day2/offline-every-read-names-the-stale-copy", func() {
		lap.Offline()
		w.AdvanceDays(1) // past live.fetch_ttl: the first read tries the remote
		lap.LoreIn(code, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march", line)
		// the next read backs off the network (devco:U-6) and still says it
		lap.LoreIn(code, "search", "--query", "vendor contract", "--json").OK(t).Says(t, `"unreachable":`, `"store": "notes"`, `"copy_of":`)
		lap.LoreIn(code, "ask", "--query", "when does the vendor contract renew").OK(t).Says(t, "renews in march", line)
		lap.LoreIn(code, "memory", "search", "--query", "vendor").OK(t).Says(t, "renews in march", line)
		lap.LoreIn(code, "search", "--query", "kubernetes migration").OK(t).Says(t, "(no matches)", "  searched: ", "  "+line)
		lap.LoreIn(code, "doctor").Says(t, "reads use this machine's copy of ")
	})

	w.Step("day2/back-online-the-line-goes", func() {
		lap.Online()
		lap.LoreIn(code, "sync").OK(t)
		lap.LoreIn(code, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march").Never(t, "unreachable")
	})
}
