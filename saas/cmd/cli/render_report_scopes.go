// render_report_scopes.go — `lore render --report --scopes` (CONTEXT_BUDGET
// 6.2.6): one row per generated file per tool, then the chain sums.
//
//	runRender (--report --scopes)
//	  ├─ root row per target: .lore/LORE.md, that tool's budget
//	  ├─ folder rows per target: toolprofile.DirTarget of each folder file
//	  │     (DirAt → <dir>/LORE.md, DirInline → <dir>/AGENTS.md block,
//	  │      DirGlobRule → the .mdc rule), budget context.budget.dir
//	  └─ chains, root to each deepest folder:
//	        DirInline (Codex) → checked against context.chain_max.codex
//	        DirAt (Claude Code) → reported only (each file loads on demand)
//
// Writes nothing, like every --report (6.11).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// flagRenderScopes adds the per-file table to --report (6.2.6).
const flagRenderScopes = "scopes"

// Statuses of a scope row or chain.
const (
	scopeStatusOK   = "ok"
	scopeStatusOver = "over"
	scopeStatusWarn = "warn"
	scopeStatusInfo = "info"
	// scopeRoot is the scope label of the root file (project + repo rows).
	scopeRoot = "project + repo"
)

// scopeFileRow is one generated file as one tool loads it.
type scopeFileRow struct {
	File     string `json:"file"`
	Scope    string `json:"scope"`
	Tool     string `json:"tool"`
	Budget   int    `json:"budget"`
	Bytes    int    `json:"bytes"`
	MustFull int    `json:"must_full"`
	MustLine int    `json:"must_line"`
	Status   string `json:"status"`
}

// scopeChain is the root-to-folder sum one tool loads for a deepest folder.
type scopeChain struct {
	Dir  string `json:"dir"`
	Tool string `json:"tool"`
	// Budget is context.chain_max.codex for a checked chain, 0 for a
	// reported-only one (Claude Code: each file has its own limit).
	Budget int    `json:"budget"`
	Parts  []int  `json:"parts"`
	Sum    int    `json:"bytes"`
	Status string `json:"status"`
}

// scopeReport is the --report --scopes body.
type scopeReport struct {
	Files  []scopeFileRow `json:"files"`
	Chains []scopeChain   `json:"chains"`
	// DirsOff is true when context.dir_files is off (CQ10).
	DirsOff bool `json:"dirs_off"`
}

// scopeStatus is ok, warn (at warn_at percent) or over.
func scopeStatus(bytes, budget, warnPct int) string {
	switch {
	case budget > 0 && bytes > budget:
		return scopeStatusOver
	case budget > 0 && bytes*percent > budget*warnPct:
		return scopeStatusWarn
	}
	return scopeStatusOK
}

// buildScopeReport builds the table from what one render computed: the root
// report, the folder plan with its bodies and reports, and the targets.
func buildScopeReport(ctx context.Context, root renderReport, plan dirPlan, bodies map[string]string,
	reps []dirReport, targets []contextTarget) scopeReport {
	set, ok := policySet(ctx)
	out := scopeReport{DirsOff: root.DirsOff}
	repOf := map[string]dirReport{}
	for _, r := range reps {
		repOf[strings.TrimPrefix(r.Scope, "dir:")] = r
	}
	paths := make([]string, 0, len(plan.Groups))
	for _, g := range plan.Groups {
		paths = append(paths, g.Path)
	}
	for _, t := range targets {
		b := profileBudget(set, ok, t.profile)
		out.Files = append(out.Files, scopeFileRow{File: root.File, Scope: scopeRoot, Tool: t.profile.Name, Budget: b,
			Bytes: root.After, MustFull: root.MustFull, MustLine: root.MustLine, Status: scopeStatus(root.After, b, root.WarnPct)})
	}
	for _, g := range plan.Groups {
		rep := repOf[g.Path]
		for _, t := range targets {
			rel, kind := t.profile.DirTarget(g.Path, dirscope.Slug(g.Path))
			if kind == toolprofile.DirNone {
				continue
			}
			file, n := rel, len(t.profile.DirContent(kind, "", g.Path, bodies[g.Path]))
			switch kind {
			case toolprofile.DirAt: // the pointer imports the folder's LORE.md
				file, n = rep.File, rep.Bytes
			case toolprofile.DirInline:
				file = rel + " (block)"
			case toolprofile.DirGlobRule, toolprofile.DirNone: // the rule file itself
			}
			out.Files = append(out.Files, scopeFileRow{File: file, Scope: rep.Scope, Tool: t.profile.Name, Budget: plan.Settings.Budget,
				Bytes: n, MustFull: rep.MustFull, MustLine: rep.MustLine, Status: scopeStatus(n, plan.Settings.Budget, root.WarnPct)})
		}
	}
	for _, g := range plan.Groups {
		if hasSubGroup(paths, g.Path) {
			continue // 6.2.6: the root-to-deepest chain
		}
		for _, t := range targets {
			_, kind := t.profile.DirTarget(g.Path, dirscope.Slug(g.Path))
			if kind != toolprofile.DirInline && kind != toolprofile.DirAt {
				continue
			}
			c := scopeChain{Dir: g.Path, Tool: t.profile.Name, Parts: []int{root.After}, Sum: root.After}
			for _, d := range dirscope.Chain(paths, g.Path) {
				n := len(bodies[d])
				if kind == toolprofile.DirInline {
					n = len(t.profile.DirContent(kind, "", d, bodies[d]))
				}
				c.Parts, c.Sum = append(c.Parts, n), c.Sum+n
			}
			if kind == toolprofile.DirInline {
				// 6.2.6: Codex adds every level into one cap.
				c.Budget = plan.Settings.ChainCodex
				c.Status = scopeStatus(c.Sum, c.Budget, root.WarnPct)
			} else {
				c.Status = scopeStatusInfo // Claude Code: reported only
			}
			out.Chains = append(out.Chains, c)
		}
	}
	return out
}

// hasSubGroup reports whether another folder file lives under dir.
func hasSubGroup(paths []string, dir string) bool {
	for _, p := range paths {
		if p != dir && dirscope.Under(p, dir) {
			return true
		}
	}
	return false
}

// text is the human table of 6.2.6.
func (s scopeReport) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-36s %-24s %-12s %7s  %7s  %-9s  %s\n", "file", "scope", "tool", "budget", "bytes", "full/line", "status")
	for _, r := range s.Files {
		st := r.Status
		if st == scopeStatusWarn || st == scopeStatusOver {
			st += fmt.Sprintf(" (%d%%)", r.Bytes*percent/max(r.Budget, 1))
		}
		fmt.Fprintf(&b, "%-36s %-24s %-12s %7d  %7d  %-9s  %s\n", r.File, r.Scope, r.Tool, r.Budget, r.Bytes,
			strconv.Itoa(r.MustFull)+"/"+strconv.Itoa(r.MustLine), st)
	}
	if s.DirsOff {
		b.WriteString("folder files: off (context.dir_files; a client repo is off until set for that client)\n")
	}
	for _, c := range s.Chains {
		parts := make([]string, 0, len(c.Parts))
		for _, p := range c.Parts {
			parts = append(parts, strconv.Itoa(p))
		}
		if c.Budget > 0 {
			fmt.Fprintf(&b, "chain %-30s %-12s budget %d  bytes %d  %s (root %s)\n", c.Dir, c.Tool, c.Budget, c.Sum, c.Status, strings.Join(parts, " + "))
		} else {
			fmt.Fprintf(&b, "chain %-30s %-12s at start + on demand: %s = %d  %s\n", c.Dir, c.Tool, strings.Join(parts, " + "), c.Sum, c.Status)
		}
	}
	return b.String()
}

// jsonBody is the --report --scopes --json body.
func (s scopeReport) jsonBody() ([]byte, error) {
	return json.MarshalIndent(map[string]scopeReport{"data": s}, "", "  ")
}
