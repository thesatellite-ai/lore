package lint

// The scope checks of `lore lint --context` (CONTEXT_BUDGET 6.2.2, 6.2.3,
// 6.12): they need the checkout's top folder, so they run beside
// contextFindings with the Env.
//
//	contextScopeFindings(ctx, all)
//	  ├─ scope_missing   a live scp_ row whose folder is gone from the checkout
//	  │                  (RepoTop upgrade; without it the check is skipped)
//	  │                  → lore scope move <old> <new>, or archive its rows
//	  └─ scope_conflict  a must rule scoped to a folder and a wider must rule
//	                     (no folder, or a folder above it) that share most
//	                     content words, with no --overrides between them
//	                     → lore rule edit <narrow> --overrides <wide>

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/scope"
	"github.com/thesatellite-ai/lore/schema"
)

// Scope finding codes.
const (
	ScopeMissing  Code = "scope_missing"
	ScopeConflict Code = "scope_conflict"
)

// conflictOverlap is the content-word Jaccard above which a narrow and a
// wide must rule "look contradictory": they talk about the same thing, so
// one probably replaces the other (6.2.2). Both rules show until someone
// says which wins, so a false hit costs one suggestion, a miss nothing more.
const conflictOverlap = 0.25

// contentWordMin is the shortest word the conflict check counts.
const contentWordMin = 4

// contextScopeFindings runs the scope checks.
func (o ops) contextScopeFindings(ctx context.Context, all map[row.Table][]sourced) []Finding {
	paths := map[string]string{} // scp_ id → folder
	var out []Finding
	top := ""
	if rt, ok := upgrade.As[scope.RepoTopper](o.env); ok {
		if t, err := rt.RepoTop(ctx); err == nil {
			top = t
		}
	}
	for _, s := range all[schema.TableScopes] {
		p := text(s.r, schema.FieldScopePath)
		paths[s.r.ID.String()] = p
		if top == "" || p == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(p))); err != nil || !fi.IsDir() {
			out = append(out, Finding{Code: ScopeMissing, ID: s.r.ID.String(), Store: string(s.src),
				Message: "folder scope " + p + " no longer exists in this checkout; its rows render into the root file",
				Fix:     "lore scope move " + p + " <new-path>"})
		}
	}
	return append(out, scopeConflicts(all[schema.TableRules], paths)...)
}

// scopeConflicts pairs a folder-scoped must rule with each wider must rule
// covering it that shares most content words and has no --overrides link.
func scopeConflicts(rules []sourced, paths map[string]string) []Finding {
	type mr struct {
		s     sourced
		dir   string
		words map[string]bool
	}
	var ms []mr
	for _, s := range rules {
		if !isPinned(schema.TableRules, s.r) || text(s.r, fSuperseded) != "" {
			continue
		}
		ms = append(ms, mr{s: s, dir: paths[text(s.r, schema.FieldScope)], words: contentWords(text(s.r, fBody))})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].s.r.ID.String() < ms[j].s.r.ID.String() })
	var out []Finding
	for _, n := range ms {
		if n.dir == "" {
			continue // only a folder rule can be the narrower one
		}
		for _, w := range ms {
			wid, nid := w.s.r.ID.String(), n.s.r.ID.String()
			if wid == nid || w.dir == n.dir || (w.dir != "" && !dirscope.Under(n.dir, w.dir)) {
				continue
			}
			if text(n.s.r, schema.FieldOverrides) == wid || text(w.s.r, schema.FieldOverrides) == nid {
				continue
			}
			if jaccard(n.words, w.words) > conflictOverlap {
				out = append(out, Finding{Code: ScopeConflict, ID: nid, Store: string(n.s.src),
					Message: "scoped to " + n.dir + " and looks like it contradicts the wider must rule " + wid + "; without --overrides both show",
					Fix:     "lore rule edit " + nid + " --overrides " + wid})
			}
		}
	}
	return out
}

// contentWords is the set of lower-case words of at least contentWordMin
// letters (short words carry no topic).
func contentWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if len(w) >= contentWordMin {
			out[w] = true
		}
	}
	return out
}
