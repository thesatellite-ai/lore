//go:build journeys && !windows

package journeys

// search_names_test.go — a person asks lore "who is alice ng" (devco
// D-791, owner P-18): the short overview row "Alice Ng (person) — CFO …"
// and the person resource alice_ng come before the long meeting notes
// whose first line also mentions her (before, those notes, holding the
// name twice, came first and the overview row was cut by --limit); and
// when a fact changed, the newest overview row answers first, even when
// it is the longer one (before, bm25 put the short old value first).

import (
	"strings"
	"testing"
)

func TestJourneySearchNamesFirst(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Doe", "dana@larkfield.example")
	lap := dana.Laptop()
	repo := lap.Clone(w.Remote("github.com/larkfield/notes"), "notes")

	long := func(n string) string {
		return "Lunch with Alice Ng and the team, notes " + n + ". Discussed the orbit rollout, the vendor contract, hiring for the data team and the office move. " +
			"Alice Ng asked about the budget line for cloud spend; follow up next week. Ng from finance (Alice) wants the invoice export by Friday. " +
			"Misc: retro actions, parking lot items, the offsite plan, the quarterly draft and the security training reminder."
	}
	w.Step("day1/notes", func() {
		lap.LoreIn(repo, "init").OK(t)
		for _, n := range []string{"1", "2", "3", "4"} {
			lap.LoreIn(repo, "memory", "add", "--body", long(n)).OK(t)
		}
		lap.LoreIn(repo, "memory", "add", "--body", "Alice Ng (person) — CFO at Acme Co; owns the budget and signs vendor contracts. Prefers email over chat and runs the monthly finance review.").OK(t)
		for _, n := range []string{"5", "6"} {
			lap.LoreIn(repo, "memory", "add", "--body", long(n)).OK(t)
		}
		lap.LoreIn(repo, "memory", "add", "--at", "2025-03-01", "--body", "Orbit (project) — status: on hold.").OK(t)
		lap.LoreIn(repo, "memory", "add", "--at", "2026-06-01", "--body", "Orbit (project) — status: shipped to every customer after the vendor fix landed; support runs the rollout checklist and the docs team publishes the guide next week.").OK(t)
	})

	w.Step("day1/the-row-naming-the-person-comes-first", func() {
		for _, q := range []string{"who is alice ng", "alice ng"} {
			out := lap.LoreIn(repo, "search", "--query", q, "--limit", "3").OK(t).Stdout
			if first := firstListedHit(t, out); !strings.Contains(first, "Alice Ng (person) — CFO") {
				t.Errorf("%q: first hit %q, want the overview row\n%s", q, first, out)
			}
			out = lap.LoreIn(repo, "memory", "search", "--query", q, "--limit", "3").OK(t).Stdout
			if first, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); !strings.Contains(first, "Alice Ng (person) — CFO") {
				t.Errorf("memory search %q: first hit %q, want the overview row\n%s", q, first, out)
			}
		}
	})

	w.Step("day1/the-newest-value-of-a-changed-fact-comes-first", func() {
		out := lap.LoreIn(repo, "search", "--query", "orbit status").OK(t).Stdout
		first := firstListedHit(t, out)
		if !strings.Contains(first, "status: shipped") {
			t.Errorf("first hit %q, want the newest status row\n%s", first, out)
		}
	})

	w.Step("day1/the-resource-named-by-the-query-comes-first", func() {
		lap.LoreIn(repo, "resource", "kind", "add", "--kind", "person").OK(t)
		lap.LoreIn(repo, "resource", "add", "--kind", "person", "--name", "alice_ng").OK(t)
		out := lap.LoreIn(repo, "search", "--query", "alice ng", "--limit", "3").OK(t).Stdout
		lines := listedHits(out)
		if len(lines) < 2 || !strings.Contains(strings.Join(lines[:2], "\n"), "alice_ng (person)") || !strings.Contains(strings.Join(lines[:2], "\n"), "Alice Ng (person) — CFO") {
			t.Errorf("first two hits %q: want the resource and the overview row\n%s", lines, out)
		}
	})
}

// listedHits is the hit lines of a `lore search` text output (the header
// and note lines left out).
func listedHits(out string) []string {
	var hits []string
	for _, l := range strings.Split(out, "\n") {
		if l == "" || strings.HasPrefix(l, "Found ") || strings.HasPrefix(l, "Showing ") || strings.HasPrefix(l, "No row ") || strings.HasPrefix(l, "note: ") || strings.HasPrefix(l, " ") {
			continue
		}
		hits = append(hits, l)
	}
	return hits
}

// firstListedHit is the first hit line of a `lore search` text output.
func firstListedHit(t *testing.T, out string) string {
	t.Helper()
	hits := listedHits(out)
	if len(hits) == 0 {
		t.Fatalf("no hit line in\n%s", out)
	}
	return hits[0]
}
