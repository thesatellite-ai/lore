// render_pathrules.go — path-scoped rule files (CONTEXT_BUDGET 6.7
// targets claude_rules and cursor_rules, D5): must rules with
// `activation: glob` leave the always-loaded core and go to the tools' own
// path loaders (Claude Code `.claude/rules/lore/lore-path-<slug>.md` with
// `paths:`, Cursor `.cursor/rules/lore-path-<slug>.mdc` with `globs:`).
//
//	planPathRules(rules, targets)
//	  ├─ some target has no path loader → nothing moves (no rule is lost to it)
//	  └─ every target has one → glob rules (root scope) grouped by glob set
//	writePathRules → one file per glob set per target; stale lore-path-* removed
//
// DECISION FA-CTX-2: glob rules leave LORE.md only when EVERY target
// profile loads path rules natively; with any other target (AGENTS.md,
// GEMINI.md, ...) they stay in the core and no path files are written, so
// no tool loses a rule and none loads it twice.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/schema"
)

// reasonPathScoped is the report reason of a glob rule moved to the path
// files (6.11 "path-scoped").
const reasonPathScoped contextfit.Reason = "path-scoped"

// pathGroup is one glob set and the rules that share it.
type pathGroup struct {
	Globs []string
	Slug  string
	Rules []pinnedRule
}

// planPathRules splits the root-level glob rules out of rules when every
// target has a path loader; it returns the rules left for the core and the
// groups (nil when nothing moves).
func planPathRules(rules []pinnedRule, profiles []toolprofile.Profile) ([]pinnedRule, []pathGroup) {
	if len(profiles) == 0 || slices.ContainsFunc(profiles, func(p toolprofile.Profile) bool { return !p.HasPathRules() }) {
		return rules, nil
	}
	var keep []pinnedRule
	byKey := map[string]int{}
	var groups []pathGroup
	for _, r := range rules {
		globs := contextfit.SplitGlobs(r.Globs)
		if r.Activation != schema.RuleActivationGlob || len(globs) == 0 || r.ScopeID != "" {
			keep = append(keep, r)
			continue
		}
		slices.Sort(globs)
		globs = slices.Compact(globs)
		key := strings.Join(globs, "\x00")
		i, ok := byKey[key]
		if !ok {
			i = len(groups)
			byKey[key] = i
			groups = append(groups, pathGroup{Globs: globs, Slug: globSlug(globs[0])})
		}
		groups[i].Rules = append(groups[i].Rules, r)
	}
	// Two glob sets may slug alike: number the later ones (deterministic:
	// groups keep the rules' load order).
	used := map[string]int{}
	for i := range groups {
		used[groups[i].Slug]++
		if n := used[groups[i].Slug]; n > 1 {
			groups[i].Slug = fmt.Sprintf("%s-%d", groups[i].Slug, n)
		}
	}
	return keep, groups
}

// globSlug turns a glob into one file-name token ("services/billing/**" →
// "services-billing").
func globSlug(g string) string {
	g = strings.Trim(strings.NewReplacer("**", "", "*", "", "?", "", "[", "", "]", "", "{", "", "}", "").Replace(g), "/.")
	if g == "" {
		return "all"
	}
	return strings.ReplaceAll(g, "/", "-")
}

// pathRuleIDs is every moved rule as a report entry.
func pathRuleIDs(groups []pathGroup) []reportEntry {
	var out []reportEntry
	for _, g := range groups {
		for _, r := range g.Rules {
			out = append(out, reportEntry{ID: r.ID, Reason: reasonPathScoped})
		}
	}
	return out
}

// pathGroupBody is the rules of one group in full, in the core's format.
func pathGroupBody(g pathGroup) string {
	var b strings.Builder
	b.WriteString("# Rules for " + strings.Join(g.Globs, ", ") + "\n\n")
	for _, r := range g.Rules {
		b.WriteString(ruleFull(r))
	}
	return b.String()
}

// writePathRules writes each group's file for every profile with a path
// loader in the registry that is a target, and removes lore's stale
// lore-path-* files (also those of profiles no longer targeted).
func writePathRules(root string, groups []pathGroup, targets []contextTarget, reg []toolprofile.Profile, quiet bool) error {
	live := map[string]bool{}
	for _, t := range targets {
		if !t.profile.HasPathRules() {
			continue
		}
		for _, g := range groups {
			rel := t.profile.PathRuleFile(g.Slug)
			live[rel] = true
			abs := filepath.Join(root, filepath.FromSlash(rel))
			body := t.profile.PathRuleContent(g.Globs, pathGroupBody(g))
			wrote, err := writeIfChanged(abs, body)
			if err != nil {
				return fmt.Errorf("write %s: %w", abs, err)
			}
			if wrote && !quiet {
				fmt.Printf("%s %s (%d bytes, paths: %s)\n", style.Success("✓"), abs, len(body), strings.Join(g.Globs, ", "))
			}
		}
	}
	seenDir := map[string]bool{}
	for _, p := range reg {
		if !p.HasPathRules() || seenDir[p.PathRules.Dir+p.PathRuleExt()] {
			continue
		}
		seenDir[p.PathRules.Dir+p.PathRuleExt()] = true
		dir := filepath.Join(root, filepath.FromSlash(p.PathRules.Dir))
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue // no folder, nothing stale
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasPrefix(name, toolprofile.PathRulePrefix) || !strings.HasSuffix(name, p.PathRuleExt()) {
				continue
			}
			rel := p.PathRules.Dir + "/" + name
			if live[rel] {
				continue
			}
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
			if !quiet {
				fmt.Printf("%s %s (removed: no glob rules for it now)\n", style.Muted("-"), filepath.Join(dir, name))
			}
		}
	}
	return nil
}
