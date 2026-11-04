package main

// TOOL NEUTRAL (owner 2026-11-04): no AI tool is lore's default. `lore init`
// wires the tools a repo already uses, else AGENTS.md; `lore render` writes
// .lore/LORE.md and keeps the pointer in each wired tool file; users add
// their own tool profiles in ~/.lore/config.yaml.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// toolRepo is a fresh git repo under w with the given files, plus helpers.
type toolRepo struct {
	t   *testing.T
	w   *world
	dir string
}

func newToolRepo(t *testing.T, files map[string]string) *toolRepo {
	t.Helper()
	w := newWorld(t)
	dir := filepath.Join(filepath.Dir(w.home), "repo")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	w.run(dir, nil, "git", "init", "-q", "-b", "main")
	r := &toolRepo{t: t, w: w, dir: dir}
	for name, body := range files {
		r.write(name, body)
	}
	return r
}

func (r *toolRepo) write(name, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *toolRepo) lore(args ...string) result {
	r.t.Helper()
	res := r.w.run(r.dir, []string{"LORE_ACTOR=human:tools@example.com"}, e2eBin, args...)
	if res.code != 0 {
		r.t.Fatalf("lore %v exited %d\nstdout: %s\nstderr: %s", args, res.code, res.stdout, res.stderr)
	}
	return res
}

// read returns the file's text, "" with ok false when it does not exist.
func (r *toolRepo) read(name string) (string, bool) {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(name)))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b), true
}

func (r *toolRepo) mustWired(name, keep string) {
	r.t.Helper()
	got, ok := r.read(name)
	if !ok || !toolprofile.HasPointer(got) {
		r.t.Fatalf("%s must carry the lore pointer: %q", name, got)
	}
	if keep != "" && !strings.Contains(got, keep) {
		r.t.Fatalf("%s lost its hand-written content: %q", name, got)
	}
}

func (r *toolRepo) mustAbsent(name string) {
	r.t.Helper()
	if _, ok := r.read(name); ok {
		r.t.Fatalf("TOOL NEUTRAL: %s must not be created", name)
	}
}

func TestToolNeutral_InitFreshRepoWritesAgentsMD(t *testing.T) {
	r := newToolRepo(t, nil)
	out := r.lore("init", "--non-interactive")
	r.mustWired("AGENTS.md", "")
	r.mustAbsent("CLAUDE.md")
	if !strings.Contains(out.stdout, "codex (AGENTS.md)") {
		t.Fatalf("init must say which tool it wired and why: %s", out.stdout)
	}
	r.lore("render")
	if body, ok := r.read(".lore/LORE.md"); !ok || strings.Contains(body, "CLAUDE.md") {
		t.Fatalf("LORE.md must exist and name no tool file: %q", body)
	}
	r.mustAbsent("CLAUDE.md")
}

func TestToolNeutral_ExistingClaudeMDKept(t *testing.T) {
	hand := "# Team notes\nKeep this.\n"
	r := newToolRepo(t, map[string]string{"CLAUDE.md": hand})
	r.lore("init", "--non-interactive")
	r.mustWired("CLAUDE.md", hand)
	r.mustAbsent("AGENTS.md")
	r.lore("render")
	r.mustWired("CLAUDE.md", hand)
	got, _ := r.read("CLAUDE.md")
	if !strings.Contains(got, "@.lore/LORE.md\n") {
		t.Fatalf("claude_code keeps the @import pointer: %q", got)
	}
	r.mustAbsent("AGENTS.md")
}

func TestToolNeutral_ClaudeAndAgentsBothWired(t *testing.T) {
	r := newToolRepo(t, map[string]string{"CLAUDE.md": "claude notes\n", "AGENTS.md": "agents notes\n"})
	r.lore("init", "--non-interactive")
	r.mustWired("CLAUDE.md", "claude notes")
	r.mustWired("AGENTS.md", "agents notes")
	r.lore("render")
	r.mustWired("CLAUDE.md", "claude notes")
	r.mustWired("AGENTS.md", "agents notes")
}

// A v0.1.17 repo wired into CLAUDE.md with a hand AGENTS.md: render keeps
// exactly CLAUDE.md and leaves AGENTS.md alone (no surprise wiring).
func TestToolNeutral_WiredRepoNotRewiredBehindItsBack(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init", "--non-interactive", "--target", "claude_code")
	r.mustWired("CLAUDE.md", "")
	r.mustAbsent("AGENTS.md")
	r.write("AGENTS.md", "hand only\n")
	out := r.lore("render")
	if !strings.Contains(out.stdout, "AGENTS.md found but not wired to lore; add it with: lore render --target codex") {
		t.Fatalf("render must name the unwired tool file: %s", out.stdout)
	}
	if got, _ := r.read("AGENTS.md"); got != "hand only\n" {
		t.Fatalf("AGENTS.md must stay untouched: %q", got)
	}
}

func TestToolNeutral_UserProfileAndRenderTarget(t *testing.T) {
	r := newToolRepo(t, nil)
	cfg := "context:\n  profiles:\n    - name: windsurf\n      pointer_file: .windsurfrules\n      import: read\n      budget: 8000\n"
	if err := os.MkdirAll(filepath.Join(r.w.home, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.w.home, ".lore", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.lore("init", "--non-interactive")
	r.mustWired("AGENTS.md", "")

	// A user profile by name.
	r.lore("render", "--target", "windsurf")
	r.mustWired(".windsurfrules", "")
	// A built-in by name: Cursor's rule file keeps its frontmatter first.
	r.lore("render", "--target", "cursor")
	mdc, _ := r.read(".cursor/rules/lore.mdc")
	if !strings.HasPrefix(mdc, "---\n") || !strings.Contains(mdc, "alwaysApply: true") || !toolprofile.HasPointer(mdc) {
		t.Fatalf("cursor rule file = %q", mdc)
	}
	// A plain render keeps every wired file, user profile included.
	plain := r.lore("render")
	if !strings.Contains(plain.stdout, ".windsurfrules (pointer present)") || !strings.Contains(plain.stdout, "lore.mdc (pointer present)") {
		t.Fatalf("plain render must keep every wired tool file: %s", plain.stdout)
	}
	r.mustWired(".windsurfrules", "")
	r.mustWired("AGENTS.md", "")
	r.mustAbsent("CLAUDE.md")

	// An invalid user profile is a config error naming the problem.
	bad := "context:\n  profiles:\n    - name: claude_code\n      pointer_file: X.md\n      import: read\n"
	if err := os.WriteFile(filepath.Join(r.w.home, ".lore", "config.yaml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	res := r.w.run(r.dir, nil, e2eBin, "render")
	if res.code == 0 || !strings.Contains(res.stderr, "E_CONFIG_INVALID") {
		t.Fatalf("bad profile: %d %s", res.code, res.stderr)
	}
	check := r.w.run(r.dir, nil, e2eBin, "config", "check")
	if check.code == 0 || !strings.Contains(check.stdout+check.stderr, "context.profiles") || !strings.Contains(check.stdout+check.stderr, "already taken") {
		t.Fatalf("config check must name the bad profile: %d %s %s", check.code, check.stdout, check.stderr)
	}
}
