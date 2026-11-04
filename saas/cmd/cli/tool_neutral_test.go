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
	// Q-R2-3: init saved context.targets = claude_code, so the way to add
	// a tool is to change that row (a one-off --target does not persist).
	if !strings.Contains(out.stdout, "AGENTS.md found but not wired to lore; add it with: lore policy set context.targets <profiles incl. codex> --yes") {
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
	// Q-R2-3: a plain render wires what the context.targets row names (init
	// saved codex); the user profile joins once the row lists it.
	r.lore("policy", "set", "context.targets", "codex,cursor,windsurf", "--yes")
	plain := r.lore("render")
	if !strings.Contains(plain.stdout, ".windsurfrules (pointer present)") || !strings.Contains(plain.stdout, "lore.mdc (pointer present)") {
		t.Fatalf("plain render must keep every tool the row names: %s", plain.stdout)
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

// policyTargets is `lore policy show context.targets`'s value column.
func (r *toolRepo) policyTargets() string {
	r.t.Helper()
	line := strings.TrimSpace(r.lore("policy", "show", "context.targets").stdout)
	f := strings.Split(line, "\t")
	if len(f) < 2 || f[0] != "context.targets" {
		r.t.Fatalf("policy show context.targets = %q", line)
	}
	return f[1]
}

// Q-R2-3 + Q-R2-4: init prints the targets it chose (no prompt) and saves
// them as the repo's context.targets row.
func TestToolNeutral_InitWritesContextTargets(t *testing.T) {
	r := newToolRepo(t, map[string]string{"CLAUDE.md": "notes\n"})
	out := r.lore("init")
	if !strings.Contains(out.stdout, "context targets: claude_code (CLAUDE.md)") || !strings.Contains(out.stdout, "saved as context.targets") {
		t.Fatalf("init must print and save its targets: %s", out.stdout)
	}
	if got := r.policyTargets(); got != "claude_code" {
		t.Fatalf("row = %q, want claude_code", got)
	}
}

// Q-R2-4: --target overrides init's choice, says so, and is what is saved.
func TestToolNeutral_InitTargetOverrides(t *testing.T) {
	o := newToolRepo(t, map[string]string{"CLAUDE.md": "notes\n"})
	flag := o.lore("init", "--target", "gemini_cli")
	if !strings.Contains(flag.stdout, "context targets: gemini_cli (GEMINI.md) (from --target)") {
		t.Fatalf("--target must override and say so: %s", flag.stdout)
	}
	if got := o.policyTargets(); got != "gemini_cli" {
		t.Fatalf("row = %q, want gemini_cli", got)
	}
	o.mustWired("GEMINI.md", "")
	if got, _ := o.read("CLAUDE.md"); toolprofile.HasPointer(got) {
		t.Fatalf("--target gemini_cli must not wire CLAUDE.md: %q", got)
	}
}

// Q-R2-3: render reads the row first: set to [codex, cursor] it writes
// those (and not the detected CLAUDE.md); an unknown profile is refused
// with E_INVALID_INPUT naming it and the row is unchanged; with the row
// removed the pointer-file fallback applies again.
func TestToolNeutral_RenderFollowsContextTargets(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init")
	r.write("CLAUDE.md", "hand\n")
	r.lore("policy", "set", "context.targets", "codex,cursor", "--yes")
	r.lore("render")
	r.mustWired("AGENTS.md", "")
	r.mustWired(".cursor/rules/lore.mdc", "")
	if got, _ := r.read("CLAUDE.md"); got != "hand\n" {
		t.Fatalf("CLAUDE.md is not in the row and must stay untouched: %q", got)
	}
	bad := r.w.run(r.dir, []string{"LORE_ACTOR=human:tools@example.com"}, e2eBin, "policy", "set", "context.targets", "codex,claud", "--yes")
	if bad.code == 0 || !strings.Contains(bad.stderr, "E_INVALID_INPUT") || !strings.Contains(bad.stderr, `"claud"`) {
		t.Fatalf("unknown profile: %d %s", bad.code, bad.stderr)
	}
	if got := r.policyTargets(); got != "codex, cursor" {
		t.Fatalf("a refused set changed the row: %q", got)
	}
	// No row: the v0.1.17 fallback (every wired pointer file, so the
	// hand CLAUDE.md, still without a pointer, is named, not wired).
	r.lore("policy", "unset", "context.targets", "--yes")
	out := r.lore("render")
	if !strings.Contains(out.stdout, "AGENTS.md (pointer present)") || !strings.Contains(out.stdout, "lore.mdc (pointer present)") ||
		!strings.Contains(out.stdout, "CLAUDE.md found but not wired to lore; add it with: lore render --target claude_code") {
		t.Fatalf("fallback render: %s", out.stdout)
	}
}
