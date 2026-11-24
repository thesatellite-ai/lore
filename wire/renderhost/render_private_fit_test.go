package renderhost

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// d909Body is a store file with n long must rules and m critical hotfixes
// (made-up text, ~550 bytes each in full), every one pinned.
func d909Body(store string, n, m int) privateBody {
	long := strings.Repeat("keep request ids, log in UTC, wrap errors with context and never print a secret value; ", 6)
	b := privateBody{head: "<!-- head of " + store + " -->\n"}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		id := fmt.Sprintf("rul_%s%028d", store[:4], i)
		b.pins = append(b.pins, privatePin{id: id, kind: privatePinRule, text: fmt.Sprintf("<rule id=%q severity=\"must\">\nRule %d: %s\n</rule>\n", id, i, long),
			lines: pinIndexLines(id, renderRuleSeverityMust, "", "", fmt.Sprintf("Rule %d: %s", i, long), ""), rank: pinRankAlways, updated: at.Add(time.Duration(i) * time.Hour)})
	}
	for i := range m {
		id := fmt.Sprintf("hfx_%s%028d", store[:4], i)
		b.pins = append(b.pins, privatePin{id: id, kind: privatePinHotfix, text: fmt.Sprintf("<hotfix id=%q severity=\"critical\">\n**Hotfix %d: the deploy breaks on a cold cache**\n\n%s\n</hotfix>\n", id, i, long),
			lines: pinIndexLines(id, renderHotfixSeverityCritical, "", fmt.Sprintf("Hotfix %d: the deploy breaks on a cold cache", i), long, ""), rank: pinRankCritical, updated: at.Add(time.Duration(i) * time.Hour)})
	}
	b.pinned = n + m
	return b
}

// TestPrivateHardBudgetD909 guards devco D-909 (owner urgent): a company
// folder with 175 long must rules and 61 critical/high hotfixes rendered a
// 146 KB private file over a 32000-byte budget, because pinned rows were
// never cut, and a tool that caps its instruction files loaded only part
// of it. The files of a repo now stay within the budget together; every
// pinned row is listed (in full while they fit, in rank order: critical
// hotfixes first, newest first; the rest as one index line of at most 120
// characters of text); the pinned text in full is still measured for the
// warning.
func TestPrivateHardBudgetD909(t *testing.T) {
	bodies := []privateBody{d909Body("acme", 150, 40), d909Body("team", 50, 20)}
	const budget = 32000
	fit := fitPrivate(bodies, budget, 40)
	total := 40 * len(bodies)
	for _, txt := range fit.texts {
		total += len(txt)
	}
	if total > budget {
		t.Fatalf("devco:D-909: the private files are %d bytes, over the budget %d (pinned rows must be cut to an index)", total, budget)
	}
	if fit.coreBytes <= budget {
		t.Fatalf("the fixture's pinned part (%d bytes) must be over the budget", fit.coreBytes)
	}
	if fit.pins.out != 0 || fit.pins.full+fit.pins.line != 260 || fit.pins.line == 0 || fit.pins.full == 0 {
		t.Fatalf("pins %+v: want all 260 listed, some in full and the rest as index lines", fit.pins)
	}
	all := strings.Join(fit.texts, "")
	for _, b := range bodies {
		for _, p := range b.pins {
			if !strings.Contains(all, "`"+p.id+"`") && !strings.Contains(all, `id="`+p.id+`"`) {
				t.Fatalf("pinned row %s is not in the files", p.id)
			}
		}
	}
	// rank: a row is in full before every index row, except a later block
	// shorter than the first one that stayed a line (fitPins: it may fill
	// the room left); critical hotfixes first
	sawLine, firstGrow := false, 0
	for i, p := range fit.ranked {
		switch {
		case p.shown == p.text && sawLine && len(p.text)-len(p.line) >= firstGrow:
			t.Fatalf("pin %d %s is in full after an index line no longer than it", i, p.id)
		case p.shown != p.text:
			if !sawLine {
				firstGrow = len(p.text) - len(p.line)
			}
			sawLine = true
			if text := strings.TrimSuffix(strings.SplitN(p.shown, "] ", 2)[1], "\n"); len([]rune(text)) > pinIndexCuts[0] {
				t.Fatalf("index text of %s is %d characters, over %d: %q", p.id, len([]rune(text)), pinIndexCuts[0], text)
			}
		}
		if i > 0 && fit.ranked[i-1].rank > p.rank {
			t.Fatalf("pins out of rank order at %d: %s after %s", i, p.id, fit.ranked[i-1].id)
		}
	}
	if fit.ranked[0].rank != pinRankCritical || fit.ranked[0].id != "hfx_acme0000000000000000000000000039" {
		t.Fatalf("first pin %s (rank %d), want the newest critical hotfix", fit.ranked[0].id, fit.ranked[0].rank)
	}
	if l := fit.pins.pinnedLine(); !strings.Contains(l, "as one-line index") || !strings.Contains(l, "`lore context --for-file <path>` / `lore show --id <id>`") {
		t.Fatalf("render line %q", l)
	}
	// SessionStart serves what the files show: within the budget too
	served := 0
	for _, p := range fit.ranked {
		served += len(p.shown)
	}
	if served > budget {
		t.Fatalf("the pinned rows the SessionStart hook serves are %d bytes, over %d", served, budget)
	}
}

// TestPrivateHardBudgetNotListedD909: when even the index lines do not
// fit, the files still stay within the budget, the first rows in rank
// order are listed and each file ends with how many more are not listed
// and the commands that list them.
func TestPrivateHardBudgetNotListedD909(t *testing.T) {
	bodies := []privateBody{d909Body("acme", 400, 100)}
	fit := fitPrivate(bodies, 8000, 0)
	if n := len(fit.texts[0]); n > 8000 {
		t.Fatalf("file is %d bytes, over 8000", n)
	}
	if fit.pins.out == 0 || !strings.Contains(fit.texts[0], "more pinned rows not listed to stay within context.budget.internal 8000 bytes: `lore rule list` and `lore hotfix list`") {
		t.Fatalf("pins %+v; no not-listed line:\n%s", fit.pins, fit.texts[0])
	}
}

// TestClampInternalBudgetD909: one ceiling for everything lore adds to a
// tool's instruction files: context.budget.internal above it (or 0, no
// budget) is clamped, and LORE.md's size comes off it.
func TestClampInternalBudgetD909(t *testing.T) {
	for _, c := range []struct{ setting, lore, want int }{
		{32000, 0, 32000},
		{200000, 0, policy.ContextCeiling},
		{0, 0, policy.ContextCeiling},
		{32000, 16000, policy.ContextCeiling - 16000},
		{32000, 3000, 32000},
		{32000, 39900, privateBudgetFloor},
	} {
		if got := clampInternalBudget(c.setting, c.lore); got != c.want {
			t.Errorf("clampInternalBudget(%d, %d) = %d, want %d", c.setting, c.lore, got, c.want)
		}
	}
	p := &privatePlan{setting: 200000, budget: policy.ContextCeiling - 3000, loreBytes: 3000}
	if l := p.clampLine(); !strings.Contains(l, "context.budget.internal 200000 clamped to 37000") || !strings.Contains(l, ".lore/LORE.md takes 3000") {
		t.Fatalf("clamp line %q", l)
	}
}

// TestLoreMDHardBudgetD909: LORE.md never exceeds its budget either: when
// every line is as short as it gets, critical hotfixes become lines and
// then pinned entries are left out from the end of the rank order, each
// named by --report and counted in the closing section; a budget set
// above the ceiling is clamped and the report says so.
func TestLoreMDHardBudgetD909(t *testing.T) {
	var hot []pinnedHotfix
	for i := range 80 {
		hot = append(hot, pinnedHotfix{ID: fmt.Sprintf("hfx_%032d", i+1), Severity: renderHotfixSeverityCritical, Title: fmt.Sprintf("Hotfix %d: the deploy breaks on a cold cache", i), Body: strings.Repeat("warm the cache first, then rerun the job. ", 10)})
	}
	in := renderInput{Settings: contextSettings{Budget: 16000, Set: 16000, Profile: toolprofile.NameCodex, EntryMax: fallbackEntryMax, WarnPct: fallbackWarnPct},
		Rules: budgetRules(150), Hotfixes: hot}
	body, rep := buildBudgetedFile("c", in)
	if rep.Over || len(body) > 16000 {
		t.Fatalf("devco:D-909: LORE.md is %d bytes over its 16000 budget (over=%v)", len(body), rep.Over)
	}
	if len(rep.PinnedOut) == 0 || !strings.Contains(body, "left out to stay within the budget: `lore render --report` names them") {
		t.Fatalf("left out %d; footer:\n%s", len(rep.PinnedOut), body[max(0, len(body)-800):])
	}
	if !strings.Contains(rep.reportText(), reasonPinnedLeftOut) {
		t.Fatal("--report does not name the pinned entries left out")
	}
	h := New(Options{Home: t.TempDir()})
	cs := h.loadContextSettings(t.Context(), []toolprofile.Profile{{Name: "wide", Budget: 100000}})
	if cs.Budget != policy.ContextCeiling || cs.Set != 100000 {
		t.Fatalf("settings budget %d set %d, want %d clamped from 100000", cs.Budget, cs.Set, policy.ContextCeiling)
	}
}
