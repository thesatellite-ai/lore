package renderhost

import (
	"strings"
	"testing"
	"time"
)

// TestPinnedRowsAsOfD952 guards devco:D-952: every rendered context file
// printed its pinned rules and hotfixes without saying when they were
// taken, so a session that loaded the file hours earlier acted on a pinned
// warning that had since been edited or retired. Each file that holds
// pinned rows now carries one line next to them: "Pinned rows as of
// <newest change among them, UTC minute>" and the command that prints a
// row's current text. The time comes from the rows, never the clock, so a
// re-render is byte-identical; a file with no pinned row has no line; the
// line is counted in the budget.
func TestPinnedRowsAsOfD952(t *testing.T) {
	ruleAt := time.Date(2026, 3, 4, 5, 6, 59, 0, time.UTC)
	hotfixAt := time.Date(2026, 3, 9, 10, 11, 12, 0, time.FixedZone("plus2", 2*60*60)) // newest: 08:11 UTC
	rules := []pinnedRule{{ID: "rul_00000000000000000000000000000001", Severity: "must", Body: "Mask card numbers in logs.", Activation: "always", Updated: ruleAt}}
	hotfixes := []pinnedHotfix{{ID: "hfx_00000000000000000000000000000001", Severity: "critical", Title: "Retries double-charge", Body: "Do not retry the charge call.", Updated: hotfixAt}}
	const want = "_Pinned rows as of 2026-03-09 08:11 UTC; a row's current text: `lore show --id <id>`._\n"

	in := squeezeInput(0, 0, 16000)
	in.Rules, in.Hotfixes = rules, hotfixes
	body, rep := buildBudgetedFile("c", in)
	if !strings.Contains(body, want+"\n## Critical hotfixes") {
		t.Fatalf("devco:D-952: LORE.md has no %q right before its pinned rows:\n%s", want, body)
	}
	if again, _ := buildBudgetedFile("c", in); again != body {
		t.Fatal("devco:D-952: a re-render with the same rows is not byte-identical")
	}
	if rep.Over {
		t.Fatalf("over the budget: %d bytes", rep.After)
	}
	if empty, _ := buildBudgetedFile("c", squeezeInput(0, 0, 16000)); strings.Contains(empty, "Pinned rows as of") {
		t.Fatal("a LORE.md with no pinned rows says when its pinned rows were taken")
	}

	if full := buildFullFile(rules, hotfixes); !strings.Contains(full, "# Project knowledge (full)\n\n"+want) {
		t.Fatalf("devco:D-952: LORE.full.md has no %q:\n%s", want, full)
	}
	if strings.Contains(buildFullFile(nil, nil), "Pinned rows as of") {
		t.Fatal("an empty LORE.full.md says when its pinned rows were taken")
	}

	dir, _ := buildDirFile(dirGroup{Path: "billing", Rules: rules, Hotfixes: hotfixes}, dirPlan{}, 8000, fallbackEntryMax)
	if !strings.Contains(dir, want+"\n## Critical hotfixes") {
		t.Fatalf("devco:D-952: the folder file has no %q before its pinned rows:\n%s", want, dir)
	}

	// The private files: one line per store file, from that file's pins,
	// and the files still fit the hard budget together.
	bodies := []privateBody{d909Body("acme", 150, 40), d909Body("team", 2, 1)}
	const budget = 32000
	fit := fitPrivate(bodies, budget, 40)
	total := 40 * len(bodies)
	for i, txt := range fit.texts {
		total += len(txt)
		line := pinnedAsOfLine(newestPin(bodies[i].pins))
		if line == "" || !strings.HasPrefix(txt, bodies[i].head+line) {
			t.Fatalf("devco:D-952: private file %d does not open with %q:\n%.400s", i, line, txt)
		}
	}
	if !strings.Contains(fit.texts[0], "as of 2026-01-07 05:00 UTC") || !strings.Contains(fit.texts[1], "as of 2026-01-01 01:00 UTC") {
		t.Fatalf("the private files' times are not each file's newest pinned row:\n%.300s\n----\n%.300s", fit.texts[0], fit.texts[1])
	}
	if total > budget {
		t.Fatalf("the private files are %d bytes with the as-of lines, over the budget %d", total, budget)
	}
	if strings.Contains(fitPrivate([]privateBody{{head: "<!-- h -->\n"}}, budget, 0).texts[0], "Pinned rows as of") {
		t.Fatal("a private file with no pinned rows says when its pinned rows were taken")
	}
}
