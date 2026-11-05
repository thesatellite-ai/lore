// doctor_targets.go — doctor's target suggestion (CONTEXT_BUDGET 6.7):
// when this machine's session rows show a coding tool working in this repo
// in the last 30 days and that tool's profile is not a render target,
// doctor names the tool and the exact policy command. Called from
// doctor.go (owned by the coordinator).
//
//	sessions (lore.db, machine-local) of the last 30 days, repo = this checkout
//	  ─► tool → profile (registry name; copilot_* → copilot)
//	  ─► not among resolveTargets(...) ─► one warning per tool
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"dbent/gen/ent"

	"saas/pkg/aicoder/sessions"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// targetSuggestWindow is how far back a session counts (6.7: 30 days).
const targetSuggestWindow = 30 * 24 * time.Hour

// copilotToolPrefix: every Copilot surface the detector names reads the
// one copilot profile's file.
const copilotToolPrefix = "copilot_"

// profileOfTool maps a detected tool name to a profile name ("" if none).
func profileOfTool(reg *toolprofile.Registry, tool string) string {
	if strings.HasPrefix(tool, copilotToolPrefix) {
		tool = toolprofile.NameCopilot
	}
	if p, ok := reg.Lookup(tool); ok {
		return p.Name
	}
	return ""
}

// doctorTargetSuggest adds one warning per tool seen here without a
// target. Any read failure is skipped (other rows report the store).
func doctorTargetSuggest(ctx context.Context, c *ent.Client, root string, r *doctorReport) {
	if coreApp == nil || c == nil || root == "" {
		return
	}
	rows, err := sessions.List(ctx, c, 0)
	if err != nil {
		return
	}
	reg, err := loadToolRegistry(ctx)
	if err != nil {
		return
	}
	pick, err := resolveTargets(ctx, root, nil, coreContextTargets)
	if err != nil {
		return
	}
	var have []string
	for _, t := range pick.targets {
		have = append(have, t.profile.Name)
	}
	since := cliClock().Now().Add(-targetSuggestWindow)
	repo := filepath.Base(root)
	seen := map[string]bool{}
	for _, s := range rows {
		if s.Repo != repo || s.StartedAt.Before(since) {
			continue
		}
		if p := profileOfTool(reg, s.Tool); p != "" && !slices.Contains(have, p) {
			seen[p] = true
		}
	}
	names := make([]string, 0, len(seen))
	for p := range seen {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s sessions seen in this repo in the last 30 days; no %s target: `lore policy set %s %s --yes`",
			p, p, policy.KeyContextTargets, strings.Join(append(slices.Clone(have), p), ",")))
	}
}
