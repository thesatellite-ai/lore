// context_forfile_scope.go — the folder-scope and topic-glob steps of
// `lore context --for-file` (CONTEXT_BUDGET 6.8 steps 2 and 4, 6.2.2,
// 6.2.8, 6.10).
//
//	contextForFile
//	  ├─ 1 globs (context_cmd.go)
//	  ├─ 2 scopeChainPicks: rules/hotfixes whose scope_id folder covers the
//	  │      path, innermost first ("scope dir:<p>"); when the calling tool
//	  │      already loaded that folder's LORE.md (claude_code, cursor,
//	  │      gemini_cli; 6.2.8) one "in context already" line instead
//	  ├─ 3 mentions (context_cmd.go)
//	  ├─ 4 topicGlobPicks: rows tagged with a tag whose globs cover the path
//	  ├─ 6 notes: scoped memories/decisions of the chain rank first
//	  └─ applyOverrides: a wider rule some picked rule overrides becomes a
//	       one-line note ("rul_… is overridden here by rul_…", 6.2.2)

package renderhost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/schema"
)

// fieldTagGlobs is a topic's glob set on its tag row (6.10). The tags
// table does not declare it yet (schema edit pending), so it is read when
// present and every tag without it simply matches nothing.
const fieldTagGlobs = "globs"

// overrideNotePrefix marks an override note's id in --json and the seen set.
const overrideNotePrefix = "overridden:"

// toolsLoadingDirFiles are the tools that load a folder's file by
// themselves when they open a file there (6.2.4, 6.2.8); codex loads only
// the folders above its start folder, so it gets the rules in full.
var toolsLoadingDirFiles = map[string]bool{
	toolprofile.NameClaudeCode: true, toolprofile.NameCursor: true, toolprofile.NameGeminiCLI: true,
}

// fileScope is what the scope steps need about one --for-file call.
type fileScope struct {
	rel   string
	chain []string          // folders covering rel, innermost first
	path  map[string]string // scp_ id → folder
	root  string            // repo top folder ("" unknown)
	tool  string            // the calling tool ("" a person / unknown)
	// pathRules is the text of the calling tool's lore-path-* rule files
	// (6.7, 6.9: the tool loads those glob rules itself).
	pathRules string
}

// newFileScope resolves the folder chain of rel; tool is the hook's tool
// or the detected one.
func (h *Host) newFileScope(ctx context.Context, rel, tool string) fileScope {
	fs := fileScope{rel: rel, path: h.scopePathsOfRepo(ctx), tool: tool}
	dirs := make([]string, 0, len(fs.path))
	for _, p := range fs.path {
		dirs = append(dirs, p)
	}
	outer := dirscope.Chain(dirs, rel)
	for i := len(outer) - 1; i >= 0; i-- {
		fs.chain = append(fs.chain, outer[i])
	}
	if top, err := runGit(ctx, false, "rev-parse", "--show-toplevel"); err == nil {
		fs.root = strings.TrimSpace(string(top))
	}
	if fs.tool == "" {
		if tool, ok := h.detectedTool(ctx); ok {
			fs.tool = tool
		}
	}
	fs.pathRules = h.toolPathRulesText(ctx, fs.root, fs.tool)
	return fs
}

// toolPathRulesText reads the lore-path-* rule files the tool loads by
// itself ("" when the tool has no path loader or none are written).
func (h *Host) toolPathRulesText(ctx context.Context, root, tool string) string {
	if root == "" || tool == "" {
		return ""
	}
	reg, err := h.loadToolRegistry(ctx)
	if err != nil {
		return ""
	}
	prof, ok := reg.Lookup(tool)
	if !ok || !prof.HasPathRules() {
		return ""
	}
	dir := filepath.Join(root, filepath.FromSlash(prof.PathRules.Dir))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), toolprofile.PathRulePrefix) {
			continue
		}
		if data, rerr := os.ReadFile(filepath.Clean(filepath.Join(dir, e.Name()))); rerr == nil {
			b.Write(data)
		}
	}
	return b.String()
}

// pathRuleLoaded reports whether the calling tool already loads row id
// through its own path rule file (6.9: the hook skips those glob matches).
func (fs fileScope) pathRuleLoaded(id string) bool {
	return fs.pathRules != "" && strings.Contains(fs.pathRules, id)
}

// folderOf is the folder a row attaches at ("" not a folder scope).
func (fs fileScope) folderOf(k knowledgeRow) string {
	return fs.path[renderField(k.r.Row, schema.FieldScope)]
}

// inChain is the chain index of a folder (-1 when not in the chain).
func (fs fileScope) inChain(dir string) int {
	for i, d := range fs.chain {
		if d == dir {
			return i
		}
	}
	return -1
}

// loaded reports whether the calling tool already has dir's folder file.
func (fs fileScope) loaded(dir string) bool {
	if !toolsLoadingDirFiles[fs.tool] || fs.root == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(fs.root, filepath.FromSlash(dir), dirFileName))
	return err == nil
}

// scopeChainPicks is step 2: the chain's rules and hotfixes, innermost
// folder first; a folder the tool already loaded is one id line.
func scopeChainPicks(fs fileScope, rows []knowledgeRow, add func(knowledgeRow, string), addLine func(contextPick)) {
	for _, dir := range fs.chain {
		var here []knowledgeRow
		for _, k := range rows {
			if (k.kind == "rule" || k.kind == "hotfix") && fs.folderOf(k) == dir {
				here = append(here, k)
			}
		}
		if len(here) == 0 {
			continue
		}
		if fs.loaded(dir) {
			ids := make([]string, 0, len(here))
			for _, k := range here {
				ids = append(ids, k.r.ID.String())
			}
			line := fmt.Sprintf("- in context already: %s/%s, %d entries (%s)\n", dir, dirFileName, len(here), strings.Join(ids, ", "))
			addLine(contextPick{ID: "dir:" + dir, Kind: "scope", Why: "loaded dir:" + dir, Bytes: len(line), full: line, line: line, group: groupRules})
			continue
		}
		for _, k := range here {
			add(k, "scope dir:"+dir)
		}
	}
}

// topicGlobPicks is step 4: rows tagged with a topic whose glob set covers
// the path.
func (h *Host) topicGlobPicks(ctx context.Context, rel string, rows []knowledgeRow, add func(knowledgeRow, string)) {
	tags, err := h.renderLiveRows(ctx, schema.TableTags)
	if err != nil {
		return // a topic lookup failure only drops step 4
	}
	topic := map[string]string{} // tag id → name, for tags whose globs cover rel
	for _, t := range tags {
		for _, g := range contextfit.SplitGlobs(renderField(t.Row, fieldTagGlobs)) {
			if contextfit.MatchGlob(g, rel) {
				topic[t.ID.String()] = renderField(t.Row, "name")
				break
			}
		}
	}
	if len(topic) == 0 {
		return
	}
	for _, k := range rows {
		if k.kind != "rule" && k.kind != "hotfix" {
			continue
		}
		for _, id := range renderTagIDs(k.r.Row) {
			if name, ok := topic[id]; ok {
				add(k, "topic "+name)
				break
			}
		}
	}
}

// applyOverrides makes every row a picked row overrides a one-line note
// (6.2.2: the narrower rule says so with --overrides; without it both
// show). A picked wider row becomes the note in place; a wider row that
// was not picked (it sits in the root core) gets the note after the rules,
// so the agent knows the core rule does not apply here.
func applyOverrides(picks []contextPick, rows []knowledgeRow) []contextPick {
	by := map[string]string{} // overridden id → overriding id
	var order []string
	in := map[string]bool{}
	for _, p := range picks {
		in[p.ID] = true
	}
	for _, k := range rows {
		if !in[k.r.ID.String()] {
			continue
		}
		if o := renderField(k.r.Row, schema.FieldOverrides); o != "" {
			if _, dup := by[o]; !dup {
				order = append(order, o)
			}
			by[o] = k.r.ID.String()
		}
	}
	note := func(wide, narrow string) contextPick {
		line := fmt.Sprintf("- `%s` is overridden here by `%s`\n", wide, narrow)
		// The note's id is not the wide row's, so the hook's seen set never
		// mutes the wide rule for files outside this folder.
		return contextPick{ID: overrideNotePrefix + wide, Kind: "override", Why: "overridden by " + narrow, Bytes: len(line), full: line, line: line, group: groupRules}
	}
	for i, p := range picks {
		if narrow, ok := by[p.ID]; ok {
			picks[i] = note(p.ID, narrow)
		}
	}
	for _, wide := range order {
		if !in[wide] {
			picks = append(picks, note(wide, by[wide]))
		}
	}
	return picks
}
