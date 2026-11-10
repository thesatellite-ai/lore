package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeModel records the call and answers a fixed reply.
type fakeModel struct {
	model, prompt string
	opts          CallOptions
	reply         string
}

func (f *fakeModel) Compress(_ context.Context, model, prompt string, o CallOptions) (Reply, error) {
	f.model, f.prompt, f.opts = model, prompt, o
	return Reply{Output: f.reply, CostUSD: 0.01, ElapsedMs: 1500}, nil
}

// bundle writes a small canonical bundle and returns its folder.
func bundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"SKILL.md": "# skill\n", "a.md": "aaa\n", "examples/e.md": "eee\n", "notes.txt": "skip\n"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCompileWritesTheDraft pins the v0.1.17 defaults (model, budget,
// draft path, globs), SKILL.md first in the prompt, and the preamble and
// code fence stripped from the reply.
func TestCompileWritesTheDraft(t *testing.T) {
	dir := bundle(t)
	m := &fakeModel{reply: "Here you go:\n```markdown\n---\nname: lore\n---\nbody\n```"}
	out, err := Compile(context.Background(), m, CompileIn{SourceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if m.model != DefaultModel || m.opts.Temperature != DefaultTemperature || m.opts.MaxTokens != maxOutputTokens {
		t.Fatalf("call %q %+v", m.model, m.opts)
	}
	if i, j := strings.Index(m.prompt, "## FILE: SKILL.md"), strings.Index(m.prompt, "## FILE: a.md"); i < 0 || j < 0 || i > j {
		t.Fatalf("SKILL.md not first: %d %d", i, j)
	}
	if strings.Contains(m.prompt, "notes.txt") || !strings.Contains(m.prompt, "examples/e.md") {
		t.Fatal("globs not applied")
	}
	want := filepath.Join(dir, "SKILL-mini-draft.md")
	b, err := os.ReadFile(filepath.Clean(want))
	if err != nil || out.Output != want || string(b) != "---\nname: lore\n---\nbody\n" {
		t.Fatalf("draft %q %q %v", out.Output, b, err)
	}
	if out.SourceFiles != 3 || out.OutputBytes != len(b) || out.CompressionX <= 0 || out.DryRun || out.Compiled != "" {
		t.Fatalf("out %+v", out)
	}
	if !strings.Contains(out.Text(), "✓ wrote "+want) {
		t.Fatalf("text %q", out.Text())
	}
}

// TestCompileDryRunWritesNothing pins --dry-run: the text comes back, no
// file is written.
func TestCompileDryRunWritesNothing(t *testing.T) {
	dir := bundle(t)
	out, err := Compile(context.Background(), &fakeModel{reply: "---\nx\n"}, CompileIn{SourceDir: dir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Compiled != "---\nx\n" || !strings.Contains(out.Text(), "BEGIN COMPILED OUTPUT (dry-run)") {
		t.Fatalf("out %+v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL-mini-draft.md")); !os.IsNotExist(err) {
		t.Fatalf("draft written on dry-run: %v", err)
	}
}

// TestCompileRefusals pins the unknown target and the empty bundle.
func TestCompileRefusals(t *testing.T) {
	if _, err := Compile(context.Background(), &fakeModel{}, CompileIn{Target: "tiny", SourceDir: bundle(t)}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("target: %v", err)
	}
	if _, err := Compile(context.Background(), &fakeModel{}, CompileIn{SourceDir: t.TempDir()}); !errs.Has(err, errs.NotFound) {
		t.Fatalf("empty: %v", err)
	}
}

// TestExtractKeepsABundlesOwnFence pins that a bundle ending in its own
// code block keeps the closing fence; only a fence opened in a preamble is
// stripped.
func TestExtractKeepsABundlesOwnFence(t *testing.T) {
	own := "---\nname: lore\n---\n```bash\nlore render\n```"
	if got := extractCompressedOutput(own); got != own+"\n" {
		t.Fatalf("own fence lost: %q", got)
	}
	if got := extractCompressedOutput("Sure, here:\n" + own); got != own+"\n" {
		t.Fatalf("plain preamble: %q", got)
	}
}

// TestSkillCompilerHelpers is ported from the old binary's
// TestUC_BenchSkillCompilerHelpers.
func TestSkillCompilerHelpers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "skill")
	write("ZETA.md", "zeta")
	write("ALPHA.md", "alpha")
	write("examples/one.md", "ex")
	write("notes.txt", "not markdown")
	if err := os.MkdirAll(filepath.Join(dir, "dir.md"), 0o750); err != nil { // a directory matching *.md
		t.Fatal(err)
	}
	files, total, err := collectSkillSources(dir, []string{"*.md", "examples/*.md", "*.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 || total != len("skill")+len("zeta")+len("alpha")+len("ex") || files["examples/one.md"] != "ex" {
		t.Fatalf("collected %v (%d bytes)", files, total)
	}
	if _, _, err := collectSkillSources(dir, []string{"["}); err == nil {
		t.Fatal("a malformed glob must error")
	}
	if f, n, err := collectSkillSources(filepath.Join(dir, "missing"), []string{"*.md"}); err != nil || len(f) != 0 || n != 0 {
		t.Fatalf("missing dir: %v %d %v", f, n, err)
	}

	prompt := buildSkillCompressPrompt(files, 9000)
	if !strings.HasPrefix(prompt, compressionRules(9000)) {
		t.Fatal("the prompt must open with the compression rules")
	}
	order := []string{"## FILE: SKILL.md", "## FILE: ALPHA.md", "## FILE: ZETA.md", "## FILE: examples/one.md"}
	last := -1
	for _, h := range order {
		i := strings.Index(prompt, h)
		if i <= last {
			t.Fatalf("SKILL.md first, then alphabetical; %q out of order", h)
		}
		last = i
	}
	if !strings.Contains(prompt, "approximately 9000 bytes") {
		t.Fatal("budget not stated")
	}
	if rules := compressionRules(1234); strings.Count(rules, "1234") != 2 || !strings.Contains(rules, "OUTPUT CONTRACT") {
		t.Fatal("compressionRules must state the budget at the top and the end")
	}
}

// TestExtractCompressedOutput is ported from the old binary's
// TestUC_BenchExtractCompressedOutput.
func TestExtractCompressedOutput(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"---\nname: x\n---\nbody":                                  "---\nname: x\n---\nbody\n",
		"  ---\nbody  \n\n":                                        "---\nbody\n",
		"```markdown\n---\nbody\n```":                              "---\nbody\n",
		"Here is the compressed version:\n---\nname: x\n---\nbody": "---\nname: x\n---\nbody\n",
		"```\nSure! Below:\n---\nbody\n```":                        "---\nbody\n",
		"ok\n---\nbody":                                            "ok\n---\nbody\n", // too little prose to strip
		"no frontmatter at all":                                    "no frontmatter at all\n",
	} {
		if got := extractCompressedOutput(in); got != want {
			t.Fatalf("extractCompressedOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── TUI tables screen ───────────────────────────────────────────────────────
