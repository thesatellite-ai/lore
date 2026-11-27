//go:build journeys && !windows

package journeys

// stale_claim_test.go — devco:D-1027 (bug hunt H-289): a person notes in
// March that the retry queue is planned and not built yet; three weeks
// later a newer note says it shipped. Both stay live, and ask printed the
// planned note as current with no word about the newer one, so an agent
// answered "not built yet". Now ask and search mark the planned note
// ("stale: a newer row says it is done: <id>") and rank it below the
// newer one, lint reports it with the command that archives it, and a
// planned note nothing contradicts stays unmarked.

import (
	"regexp"
	"strings"
	"testing"
)

// staleClaimMemID is the memory id a `memory add --json` printed.
func staleClaimMemID(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`"id": "(mem_[0-9a-f]{32})"`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no memory id in: %s", out)
	}
	return m[1]
}

func TestJourneyPlannedNoteContradictedByNewerNote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")
	var planned, shipped string

	w.Step("march/planned-then-shipped", func() {
		lap.LoreIn(code, "init").OK(t)
		planned = staleClaimMemID(t, lap.LoreIn(code, "memory", "add", "--to", "repo", "--at", "2026-03-01", "--body", "Planned: a retry queue for failed webhook deliveries (not built yet).", "--json").OK(t).Out())
		shipped = staleClaimMemID(t, lap.LoreIn(code, "memory", "add", "--to", "repo", "--at", "2026-03-20", "--body", "The retry queue for webhook deliveries shipped in v0.4.", "--json").OK(t).Out())
		lap.LoreIn(code, "memory", "add", "--to", "repo", "--at", "2026-03-21", "--body", "Planned: CSV export for the billing page.").OK(t)
	})

	mark := "[stale: a newer row says it is done: "
	w.Step("ask-and-search-mark-the-planned-note", func() {
		ask := lap.LoreIn(code, "ask", "--query", "is the webhook retry queue built").OK(t).Out()
		if !strings.Contains(ask, mark+shipped+" (2026-03-20)]") {
			t.Fatalf("ask does not mark the planned note:\n%s", ask)
		}
		if strings.Index(ask, "shipped in v0.4") > strings.Index(ask, "Planned: a retry queue") {
			t.Errorf("the planned note is printed before the newer note that says it shipped:\n%s", ask)
		}
		lap.LoreIn(code, "ask", "--query", "is the webhook retry queue built", "--json").OK(t).Says(t, `"contradicted_by": "`+shipped+`"`)
		search := lap.LoreIn(code, "search", "--query", "retry queue").OK(t).Out()
		if !strings.Contains(search, mark+shipped) {
			t.Fatalf("search does not mark the planned note:\n%s", search)
		}
		csv := lap.LoreIn(code, "ask", "--query", "billing CSV export").OK(t).Out()
		if strings.Contains(csv, mark) {
			t.Errorf("a planned note nothing contradicts is marked:\n%s", csv)
		}
	})

	w.Step("lint-reports-it-and-its-fix-clears-it", func() {
		lap.LoreIn(code, "lint", "--code", "stale_claim").OK(t).Says(t, "stale_claim "+planned, "the newer "+shipped)
		lap.LoreIn(code, "memory", "archive", "--id", planned).OK(t)
		lap.LoreIn(code, "lint", "--code", "stale_claim").OK(t).Says(t, "no findings")
	})
}
