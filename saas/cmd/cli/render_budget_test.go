package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// budgetRules makes n must rules with long bodies (summary on the even
// ones, activation always on the first).
func budgetRules(n int) []pinnedRule {
	out := make([]pinnedRule, n)
	for i := range out {
		out[i] = pinnedRule{ID: fmt.Sprintf("rul_%032d", i+1), Severity: "must", TrustScore: 0.9,
			Body:    fmt.Sprintf("rule %d: ", i) + strings.Repeat("always check the payment ledger before refunds ", 8),
			Updated: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)}
		if i%2 == 0 {
			out[i].Summary = fmt.Sprintf("rule %d short form", i)
		}
	}
	if n > 0 {
		out[0].Activation = schema.RuleActivationAlways
	}
	return out
}

// TestBuildBudgetedFileMatrix (CONTEXT_BUDGET 6.3-6.6, CQ3, OV18/OV25):
// across budgets from tiny to unlimited and 0..12 must rules, every must
// rule is in the file (full or one line, never dropped); a critical hotfix
// is always in full and a high one always one line; MustFull+MustLine is
// the rule count; a big budget shows every rule in full; the budget flags
// say whether the file went over; the same input renders byte-identical
// output twice.
func TestBuildBudgetedFileMatrix(t *testing.T) {
	crit := pinnedHotfix{ID: "hfx_" + strings.Repeat("1", 32), Severity: "critical", Title: "Prod DB read-only", Body: strings.Repeat("do not write to prod ", 10)}
	high := pinnedHotfix{ID: "hfx_" + strings.Repeat("2", 32), Severity: "high", Title: "Cache stale", Summary: "flush cache after deploy", Body: strings.Repeat("flush ", 40)}
	for _, n := range []int{0, 1, 5, 12} {
		for _, budget := range []int{500, 2000, 6000, 1 << 20} {
			t.Run(fmt.Sprintf("rules=%d/budget=%d", n, budget), func(t *testing.T) {
				in := renderInput{
					Settings: contextSettings{Budget: budget, Profile: toolprofile.NameCodex, EntryMax: fallbackEntryMax, WarnPct: fallbackWarnPct},
					Rules:    budgetRules(n), Hotfixes: []pinnedHotfix{crit, high},
					Topics: []topicLine{{Name: "payments", Counts: map[string]int{"rules": n}}},
				}
				body, rep := buildBudgetedFile("c1", in)
				body2, _ := buildBudgetedFile("c1", in)
				if body != body2 {
					t.Fatal("render is not deterministic")
				}
				for _, r := range in.Rules {
					if !strings.Contains(body, r.ID) {
						t.Fatalf("must rule %s dropped from the file", r.ID)
					}
				}
				if rep.MustFull+rep.MustLine != n || len(rep.Lines) != rep.MustLine {
					t.Fatalf("full %d + line %d != %d (lines %d)", rep.MustFull, rep.MustLine, n, len(rep.Lines))
				}
				if !strings.Contains(body, hotfixFull(crit)) {
					t.Fatal("a critical hotfix must be in full")
				}
				if !strings.Contains(body, hotfixLine(high)) || strings.Contains(body, hotfixFull(high)) {
					t.Fatal("a high hotfix must be one line")
				}
				if budget == 1<<20 && rep.MustLine != 0 {
					t.Fatalf("an unlimited budget shortened %d rules", rep.MustLine)
				}
				if rep.After != len(body) || rep.Over != (len(body) > budget) {
					t.Fatalf("report after=%d over=%v for %d bytes, budget %d", rep.After, rep.Over, len(body), budget)
				}
				if !strings.HasPrefix(body, "<!-- AICODER:CANARY=c1 ") {
					t.Fatal("canary not on the first line")
				}
				if strings.Contains(body, "No pinned knowledge yet") {
					t.Fatal("the empty-state line shows while hotfixes are pinned")
				}
			})
		}
	}
}

// TestBuildBudgetedFileLinesWhenTight: with a budget that cannot hold the
// rules in full some become lines, the "Not in this file" footer counts
// them, and an always-on rule is the last to lose its full text.
func TestBuildBudgetedFileLinesWhenTight(t *testing.T) {
	rules := budgetRules(10)
	in := renderInput{Settings: contextSettings{Budget: 3000, Profile: toolprofile.NameCodex, EntryMax: fallbackEntryMax, WarnPct: fallbackWarnPct}, Rules: rules}
	body, rep := buildBudgetedFile("c", in)
	if rep.MustLine == 0 {
		t.Fatalf("expected line forms at budget 3000 (after %d)", rep.After)
	}
	if !strings.Contains(body, fmt.Sprintf("- %d pinned entries are shown here only as one line", rep.MustLine)) {
		t.Fatal("footer does not count the line forms")
	}
	if rep.MustFull > 0 && !strings.Contains(body, ruleFull(rules[0])) {
		t.Fatal("the always-on rule lost its full text while others kept theirs")
	}
}

// TestProfileBudget (CQ1, CQ6): context.budget.<profile> beats the
// profile's own budget, which beats context.budget.default; without a
// policy the profile's effective budget applies.
func TestProfileBudget(t *testing.T) {
	codex := toolprofile.Profile{Name: toolprofile.NameCodex}
	own := toolprofile.Profile{Name: "windsurf", Budget: 7777}
	set := func(t *testing.T, kv map[policy.Key]int64) policy.Set {
		t.Helper()
		var es []policy.Entry
		for k, v := range kv {
			es = append(es, policy.Entry{Level: policy.LevelRepo, Key: k, Value: value.Int(v)})
		}
		s, err := policy.Resolve(es)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := profileBudget(policy.Set{}, false, codex); got != codex.EffectiveBudget() {
		t.Fatalf("no policy: %d", got)
	}
	if got := profileBudget(policy.Set{}, false, own); got != 7777 {
		t.Fatalf("no policy, own budget: %d", got)
	}
	s := set(t, map[policy.Key]int64{"context.budget.codex": 4000, policy.KeyContextBudgetDefault: 9000})
	if got := profileBudget(s, true, codex); got != 4000 {
		t.Fatalf("profile key: %d", got)
	}
	if got := profileBudget(s, true, own); got != 7777 {
		t.Fatalf("own budget beats the default: %d", got)
	}
	if got := profileBudget(s, true, toolprofile.Profile{Name: "mine"}); got != 9000 {
		t.Fatalf("default key: %d", got)
	}
}

// TestBuildBudgetedFileEmpty: nothing pinned prints the empty-state line.
func TestBuildBudgetedFileEmpty(t *testing.T) {
	body, rep := buildBudgetedFile("c", renderInput{Settings: contextSettings{Budget: 4000, Profile: toolprofile.NameCodex, EntryMax: fallbackEntryMax, WarnPct: fallbackWarnPct}})
	if !strings.Contains(body, "No pinned knowledge yet") || rep.MustFull+rep.MustLine != 0 || rep.Over {
		t.Fatalf("empty render: %+v", rep)
	}
}
