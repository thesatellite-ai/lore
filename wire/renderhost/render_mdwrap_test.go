package renderhost

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// tagLine is a line that opens or closes one of the <rule>/<hotfix>/…
// reference blocks a render wraps each row in: an HTML block, not prose.
var tagLine = regexp.MustCompile(`^</?[a-z_]+(?:\s[^>]*)?>\s*$`)

// wrapProblems is mdwrap.Check of text minus the lines that only touch a
// row's tag block: the opening tag line, the line right after it (the
// row's first line), and the closing tag line. What is inside the tags is
// the person's row text.
func wrapProblems(text string) []mdwrap.Problem {
	lines := strings.Split(text, "\n")
	var out []mdwrap.Problem
	for _, p := range mdwrap.Check(text) {
		cur := strings.TrimSpace(p.Text)
		prev := ""
		if p.Line >= 2 {
			prev = strings.TrimSpace(lines[p.Line-2])
		}
		if tagLine.MatchString(cur) || tagLine.MatchString(prev) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// checkMarkdownUnder runs wrapProblems on every .md file under dir (not
// .git, not the row files) and returns how many it read.
func checkMarkdownUnder(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "data") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		n++
		if ps := wrapProblems(string(b)); len(ps) > 0 {
			t.Errorf("%s is hard-wrapped:\n%s", p, mdwrap.Lines(ps))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRenderNotHardWrapped: every Markdown file a render writes
// (.lore/LORE.md, LORE.full.md, the pointer blocks in CLAUDE.md /
// AGENTS.md, the private render files and their index under the lore
// home) keeps one physical line per paragraph, bullet and table cell. The
// rows hold one-line text, so any continued line is lore's own text.
func TestRenderNotHardWrapped(t *testing.T) {
	ctx := context.Background()
	h := opstest.New(t)
	for _, f := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(h.Repo, f), []byte("# Notes\n\nOne paragraph.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tb := range []row.Table{schema.TableRules, schema.TableHotfixes, schema.TableMemories, schema.TableDecisions,
		schema.TablePatterns, schema.TablePlaybooks, schema.TableTastePrefs, schema.TableWorkflows, schema.TableBehaviours} {
		set := map[string]string{}
		switch tb {
		case schema.TableRules:
			set["severity"] = `"must"`
		case schema.TableHotfixes:
			set["severity"] = `"critical"`
		}
		h.Seed(tb, set)
	}
	h.Seed(schema.TableHotfixes, map[string]string{"severity": `"high"`})
	// An empty repo too: its LORE.md ends with the "no pinned knowledge"
	// paragraph after the footer's bullets.
	empty := opstest.New(t)
	for _, r := range []*opstest.H{h, empty} {
		t.Chdir(r.Repo) // render resolves the repo root from the working folder
		host := New(Options{App: r.App, Home: t.TempDir()})
		if _, err := host.Run(ctx, Flags{OutPath: filepath.Join(r.Repo, filepath.FromSlash(LoreMDRel)), Quiet: true}); err != nil {
			t.Fatal(err)
		}
		if n := checkMarkdownUnder(t, r.Repo); n < 2 { // at least LORE.md and LORE.full.md
			t.Fatalf("checked only %d markdown files in the repo", n)
		}
	}

	// The private render: an internal store's rows go to files under the
	// lore home, plus their index.
	a, repo := privApp(t)
	home := t.TempDir()
	var w strings.Builder
	if err := New(Options{App: a, Home: home}).renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	if n := checkMarkdownUnder(t, filepath.Join(home, renderDirName)); n < 2 { // a store file and the index
		t.Fatalf("checked only %d private render files", n)
	}
}

// TestFolderAndPathFilesNotHardWrapped: the folder files (LORE.md per
// folder and the Codex AGENTS.md block), the root index's folder lines and
// the path-rule files keep one physical line per paragraph, bullet and
// cell, at a roomy budget (rows in full) and a tight one (line forms).
func TestFolderAndPathFilesNotHardWrapped(t *testing.T) {
	crit := pinnedHotfix{ID: "hfx_crit", Severity: renderHotfixSeverityCritical, Title: "pool leak", Body: "close rows"}
	high := pinnedHotfix{ID: "hfx_high", Severity: renderHotfixSeverityHigh, Title: "retry storm", Body: "cap retries"}
	groups := []dirGroup{scopeGroup("src", 3), scopeGroup("src/api", 4, crit, high)}
	texts := map[string]string{"folder index": dirIndexText(groups)}
	for _, budget := range []int{0, 2200} {
		plan := dirPlan{Settings: dirSettings{On: true, Budget: budget, CodexBudget: budget, MinEntries: 1}, Groups: groups,
			Overrides: map[string]string{"rul_src_api_00": "rul_root_wide"}}
		bodies, _ := buildDirFiles(plan, fallbackEntryMax)
		for p, s := range bodies.File {
			texts["folder file "+p+" at "+strconv.Itoa(budget)] = s
		}
		for p, s := range bodies.Inline {
			texts["folder block "+p+" at "+strconv.Itoa(budget)] = s
		}
	}
	g := pathGroup{Globs: []string{"src/**", "lib/**"}, Slug: "src", Rules: groups[1].Rules}
	reg, err := toolprofile.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{0, 600} {
		body := pathGroupBody(g, budget)
		for _, p := range reg.Profiles() {
			if p.PathRules.Style != toolprofile.PathRulesNone && p.PathRules.Style != "" {
				texts["path rules "+p.Name+" at "+strconv.Itoa(budget)] = p.PathRuleContent(g.Globs, body)
			}
		}
	}
	if len(texts) < 8 { // index + 2 folders x 2 budgets (file) + blocks + path rules
		t.Fatalf("only %d texts built", len(texts))
	}
	for name, s := range texts {
		if ps := wrapProblems(s); len(ps) > 0 {
			t.Errorf("%s is hard-wrapped:\n%s", name, mdwrap.Lines(ps))
		}
	}
}
