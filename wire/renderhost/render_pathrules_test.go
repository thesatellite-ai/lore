package renderhost

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/schema"
)

// builtin is the built-in profile named n.
func builtin(t *testing.T, n string) toolprofile.Profile {
	t.Helper()
	for _, p := range toolprofile.Builtins() {
		if p.Name == n {
			return p
		}
	}
	t.Fatalf("no built-in profile %s", n)
	return toolprofile.Profile{}
}

// TestPathRulesMatrix (CONTEXT_BUDGET 6.7, 6.9, FA-CTX-2; fa-ctx
// later:46): glob rules leave the core only when every target has a path
// loader (claude_code, cursor, both); with a mixed or empty target set
// nothing moves. Rules sharing a glob set (any order, duplicates) share a
// group; two sets that slug alike are numbered; folder-scoped, empty-glob
// and always rules stay. Written files are removed once their group is
// gone, also for a profile no longer targeted; the hook skips a glob rule
// the calling tool loads from its own path file and keeps the others.
func TestPathRulesMatrix(t *testing.T) {
	glob := func(id, globs string) pinnedRule {
		return pinnedRule{ID: id, Severity: renderRuleSeverityMust, Body: "body of " + id, Activation: schema.RuleActivationGlob, Globs: globs}
	}
	rules := []pinnedRule{
		glob("rul_a", `["src/**","lib/**"]`),
		glob("rul_b", `["lib/**","src/**","src/**"]`), // same set as rul_a
		glob("rul_c", `["web/**"]`),
		glob("rul_d", `["src/*"]`), // slugs like "src/**"
		glob("rul_e", `["src/**"]`),
		{ID: "rul_f", Severity: renderRuleSeverityMust, Body: "always", Activation: schema.RuleActivationAlways},
		glob("rul_g", `[]`),
	}
	scoped := glob("rul_h", `["api/**"]`)
	scoped.ScopeID = "scp_1"
	rules = append(rules, scoped)

	claude, cursor, codex := builtin(t, toolprofile.NameClaudeCode), builtin(t, toolprofile.NameCursor), builtin(t, toolprofile.NameCodex)
	for _, c := range []struct {
		name    string
		targets []toolprofile.Profile
		moves   bool
	}{
		{"claude_code", []toolprofile.Profile{claude}, true},
		{"cursor", []toolprofile.Profile{cursor}, true},
		{"claude_code+cursor", []toolprofile.Profile{claude, cursor}, true},
		{"claude_code+codex (mixed)", []toolprofile.Profile{claude, codex}, false},
		{"codex", []toolprofile.Profile{codex}, false},
		{"no targets", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			keep, groups := planPathRules(rules, c.targets)
			if !c.moves {
				if len(groups) != 0 || len(keep) != len(rules) {
					t.Fatalf("moved %d groups, kept %d of %d", len(groups), len(keep), len(rules))
				}
				return
			}
			var kept []string
			for _, r := range keep {
				kept = append(kept, r.ID)
			}
			if !slices.Equal(kept, []string{"rul_f", "rul_g", "rul_h"}) {
				t.Fatalf("kept %v", kept)
			}
			var got []string
			for _, g := range groups {
				var ids []string
				for _, r := range g.Rules {
					ids = append(ids, r.ID)
				}
				got = append(got, g.Slug+"="+strings.Join(ids, ","))
			}
			want := []string{"lib=rul_a,rul_b", "web=rul_c", "src=rul_d", "src-2=rul_e"}
			if !slices.Equal(got, want) {
				t.Fatalf("groups %v, want %v", got, want)
			}
			if n := len(pathRuleIDs(groups)); n != 5 {
				t.Fatalf("%d path-scoped report entries, want 5", n)
			}
		})
	}

	// Files: write for claude_code + cursor, then for claude_code alone
	// with one group left: the rest is removed for both tools.
	ctx := context.Background()
	a, _ := repoApp(t)
	h := New(Options{App: a})
	root := t.TempDir()
	reg := toolprofile.Builtins()
	_, groups := planPathRules(rules, []toolprofile.Profile{claude, cursor})
	var w strings.Builder
	both := []contextTarget{{profile: claude}, {profile: cursor}}
	if err := h.writePathRules(&w, root, groups, both, reg, fallbackPathFileBudget, true); err != nil {
		t.Fatal(err)
	}
	files := func(p toolprofile.Profile) []string {
		ents, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(p.PathRules.Dir)))
		var out []string
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), toolprofile.PathRulePrefix) {
				out = append(out, e.Name())
			}
		}
		return out
	}
	if len(files(claude)) != 4 || len(files(cursor)) != 4 {
		t.Fatalf("claude %v cursor %v, want 4 each", files(claude), files(cursor))
	}
	if !slices.Contains(files(claude), "lore-path-src-2.md") || !slices.Contains(files(cursor), "lore-path-src-2.mdc") {
		t.Fatalf("numbered slug missing: %v %v", files(claude), files(cursor))
	}

	// Hook skip (6.9): claude_code loads rul_a from its path file; codex does not.
	loaded := fileScope{pathRules: h.toolPathRulesText(ctx, root, toolprofile.NameClaudeCode)}
	if !loaded.pathRuleLoaded("rul_a") || !loaded.pathRuleLoaded("rul_e") || loaded.pathRuleLoaded("rul_f") {
		t.Fatalf("claude_code path rules text: %q", loaded.pathRules)
	}
	if txt := h.toolPathRulesText(ctx, root, toolprofile.NameCodex); txt != "" {
		t.Fatalf("codex has no path loader, got %q", txt)
	}

	if err := h.writePathRules(&w, root, groups[:1], []contextTarget{{profile: claude}}, reg, fallbackPathFileBudget, true); err != nil {
		t.Fatal(err)
	}
	if got := files(claude); !slices.Equal(got, []string{"lore-path-lib.md"}) {
		t.Fatalf("claude after shrink %v", got)
	}
	if got := files(cursor); len(got) != 0 {
		t.Fatalf("cursor no longer targeted, stale files kept: %v", got)
	}
	// A user's own file in the rules folder is never touched.
	own := filepath.Join(root, ".claude", "rules", "lore", "mine.md")
	if err := os.WriteFile(own, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.writePathRules(&w, root, nil, []contextTarget{{profile: claude}}, reg, fallbackPathFileBudget, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(own); err != nil || len(files(claude)) != 0 {
		t.Fatalf("own file %v, lore files %v", err, files(claude))
	}
}
