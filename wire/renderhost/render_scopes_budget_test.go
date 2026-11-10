package renderhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// scopeGroup is a folder group of n must rules (each body ~600 bytes) and
// the given hotfixes.
func scopeGroup(path string, n int, hotfixes ...pinnedHotfix) dirGroup {
	g := dirGroup{Path: path, Hotfixes: hotfixes}
	for i := range n {
		g.Rules = append(g.Rules, pinnedRule{ID: fmt.Sprintf("rul_%s_%02d", strings.ReplaceAll(path, "/", "_"), i), Severity: renderRuleSeverityMust,
			Body: strings.Repeat("validate every input at the edge. ", 18), Summary: fmt.Sprintf("rule %d of %s", i, path), Activation: schema.RuleActivationAlways})
	}
	return g
}

// TestDirFileBudgetMatrix (CONTEXT_BUDGET 6.2.6, 6.3; OV25 budgets x
// scopes): every folder file over budget {unlimited, roomy, tight, below
// its fixed part} x group shape {rules only, critical + high hotfixes,
// parent with a subfolder, an override} keeps every must rule (full or
// line, never dropped), every critical hotfix in full and every high one
// as a line; its report counts match the text, budget_exceeded is set
// exactly when a positive budget is passed, and two builds are identical.
func TestDirFileBudgetMatrix(t *testing.T) {
	crit := pinnedHotfix{ID: "hfx_crit", Severity: renderHotfixSeverityCritical, Title: "pool leak", Body: "close rows"}
	high := pinnedHotfix{ID: "hfx_high", Severity: renderHotfixSeverityHigh, Title: "retry storm", Body: "cap retries"}
	shapes := []struct {
		name   string
		groups []dirGroup
		over   map[string]string
	}{
		{name: "rules only", groups: []dirGroup{scopeGroup("src/api", 5)}},
		{name: "critical and high hotfixes", groups: []dirGroup{scopeGroup("src/api", 4, crit, high)}},
		{name: "parent and subfolder", groups: []dirGroup{scopeGroup("src", 3), scopeGroup("src/api", 3, high)}},
		{name: "override", groups: []dirGroup{scopeGroup("src/api", 3)}, over: map[string]string{"rul_src_api_00": "rul_root_wide"}},
	}
	budgets := []struct {
		name     string
		budget   int
		allFull  bool // every must rule in full
		someLine bool // at least one must rule as a line
	}{
		{name: "unlimited", budget: 0, allFull: true},
		{name: "roomy", budget: 50000, allFull: true},
		{name: "tight", budget: 2200, someLine: true},
		{name: "below fixed", budget: 100, someLine: true},
	}
	for _, s := range shapes {
		for _, bc := range budgets {
			t.Run(s.name+"/"+bc.name, func(t *testing.T) {
				over := s.over
				if over == nil {
					over = map[string]string{}
				}
				plan := dirPlan{Settings: dirSettings{On: true, Budget: bc.budget, CodexBudget: bc.budget, MinEntries: 1}, Groups: s.groups, Overrides: over}
				bodies, reps := buildDirFiles(plan, fallbackEntryMax)
				again, _ := buildDirFiles(plan, fallbackEntryMax)
				if len(reps) != len(s.groups) {
					t.Fatalf("%d reports for %d groups", len(reps), len(s.groups))
				}
				for i, g := range s.groups {
					body, rep := bodies.File[g.Path], reps[i]
					if body != again.File[g.Path] || bodies.Inline[g.Path] != again.Inline[g.Path] {
						t.Fatal("two builds differ")
					}
					if rep.Bytes != len(body) || rep.Budget != bc.budget || rep.Scope != "dir:"+g.Path {
						t.Fatalf("report %+v for %d bytes", rep, len(body))
					}
					if rep.Over != (bc.budget > 0 && len(body) > bc.budget) {
						t.Fatalf("budget_exceeded %v at %d/%d", rep.Over, len(body), bc.budget)
					}
					if rep.MustFull+rep.MustLine != len(g.Rules) {
						t.Fatalf("full %d + line %d != %d must rules", rep.MustFull, rep.MustLine, len(g.Rules))
					}
					if bc.allFull && rep.MustLine != 0 {
						t.Fatalf("%d line forms under budget %d", rep.MustLine, bc.budget)
					}
					if bc.someLine && rep.MustLine == 0 {
						t.Fatalf("no line forms under budget %d (%d bytes)", bc.budget, len(body))
					}
					for _, r := range g.Rules {
						if !strings.Contains(body, r.ID) {
							t.Fatalf("must rule %s dropped", r.ID)
						}
					}
					for _, h := range g.Hotfixes {
						switch h.Severity {
						case renderHotfixSeverityCritical:
							if !strings.Contains(body, hotfixFull(h)) {
								t.Fatalf("critical %s not in full", h.ID)
							}
						default:
							if !strings.Contains(body, hotfixLine(h)) || strings.Contains(body, hotfixFull(h)) {
								t.Fatalf("high %s not a line", h.ID)
							}
						}
					}
					if g.Path == "src" && len(s.groups) > 1 && !strings.Contains(body, "- subfolder src/api has its own file") {
						t.Fatal("parent file does not name its subfolder")
					}
					for id, wide := range over {
						if strings.Contains(body, id) && !strings.Contains(body, "`"+wide+"` is replaced in this folder by `"+id+"`") {
							t.Fatal("override line missing")
						}
					}
				}
			})
		}
	}
}
