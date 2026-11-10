package renderhost

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestScopeStatus pins the 6.2.6 statuses: over the budget, past warn_at, ok,
// and no budget at all.
func TestScopeStatus(t *testing.T) {
	cases := []struct {
		bytes, budget, warn int
		want                string
	}{
		{9000, 8000, 80, scopeStatusOver},
		{7000, 8000, 80, scopeStatusWarn},
		{6400, 8000, 80, scopeStatusOK},
		{99999, 0, 80, scopeStatusOK},
	}
	for _, c := range cases {
		if got := scopeStatus(c.bytes, c.budget, c.warn); got != c.want {
			t.Errorf("scopeStatus(%d,%d,%d) = %q, want %q", c.bytes, c.budget, c.warn, got, c.want)
		}
	}
}

// TestHasSubGroup: only a folder with no folder file under it ends a chain.
func TestHasSubGroup(t *testing.T) {
	paths := []string{"src/booking", "src/booking/payments", "src/bookingx"}
	if !hasSubGroup(paths, "src/booking") {
		t.Error("src/booking has src/booking/payments under it")
	}
	if hasSubGroup(paths, "src/booking/payments") || hasSubGroup(paths, "src/bookingx") {
		t.Error("leaf folders have nothing under them")
	}
}

// TestBuildScopeReport (CONTEXT_BUDGET 6.2.6, OV25 `render --report
// --scopes`): one root row per target with that tool's budget, one row per
// folder per target (Claude Code: the folder LORE.md under
// context.budget.dir; Codex: the AGENTS.md block under dir.codex), and a
// chain only for the deepest folder: Codex's checked against
// chain_max.codex with every level summed, Claude Code's reported only.
func TestBuildScopeReport(t *testing.T) {
	claude, codex := builtin(t, toolprofile.NameClaudeCode), builtin(t, toolprofile.NameCodex)
	plan := dirPlan{Settings: dirSettings{On: true, Budget: 20000, CodexBudget: 2000, ChainCodex: 3000, MinEntries: 1},
		Groups: []dirGroup{scopeGroup("src", 3), scopeGroup("src/api", 3)}, Overrides: map[string]string{}}
	bodies, reps := buildDirFiles(plan, fallbackEntryMax)
	root := renderReport{File: ".lore/LORE.md", After: 1000, MustFull: 2, WarnPct: fallbackWarnPct}
	for _, off := range []bool{false, true} {
		root.DirsOff = off
		rep := New(Options{}).buildScopeReport(context.Background(), root, plan, bodies, reps, []contextTarget{{profile: claude}, {profile: codex}})
		if len(rep.Files) != 6 {
			t.Fatalf("%d file rows, want 6: %+v", len(rep.Files), rep.Files)
		}
		for _, r := range rep.Files {
			switch {
			case r.Scope == scopeRoot:
				if r.File != ".lore/LORE.md" || r.Bytes != 1000 || r.Budget != profileBudget(policy.Set{}, false, builtin(t, r.Tool)) {
					t.Fatalf("root row %+v", r)
				}
			case r.Tool == toolprofile.NameCodex:
				if !strings.HasSuffix(r.File, "(block)") || r.Budget != 2000 || r.MustFull+r.MustLine != 3 {
					t.Fatalf("codex folder row %+v", r)
				}
			default:
				if r.File != strings.TrimPrefix(r.Scope, "dir:")+"/"+dirFileName || r.Budget != 20000 || r.MustFull != 3 || r.Status != scopeStatusOK {
					t.Fatalf("claude folder row %+v", r)
				}
			}
		}
		if len(rep.Chains) != 2 {
			t.Fatalf("chains %+v, want the deepest folder per tool", rep.Chains)
		}
		for _, c := range rep.Chains {
			if c.Dir != "src/api" || len(c.Parts) != 3 || c.Parts[0] != 1000 {
				t.Fatalf("chain %+v", c)
			}
			sum := 0
			for _, p := range c.Parts {
				sum += p
			}
			switch c.Tool {
			case toolprofile.NameCodex:
				if c.Budget != 3000 || c.Sum != sum || c.Status != scopeStatus(sum, 3000, fallbackWarnPct) {
					t.Fatalf("codex chain %+v", c)
				}
			default:
				if c.Budget != 0 || c.Status != scopeStatusInfo {
					t.Fatalf("claude chain %+v", c)
				}
			}
		}
		txt := rep.text()
		if !strings.Contains(txt, "chain src/api") || strings.Contains(txt, "folder files: off") != off {
			t.Fatalf("text (off=%v):\n%s", off, txt)
		}
		if b, err := rep.jsonBody(); err != nil || !strings.Contains(string(b), `"chains"`) {
			t.Fatalf("json %s %v", b, err)
		}
	}
}
