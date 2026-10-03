package gitsetup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	// macOS temp dirs are symlinked; git reports the resolved path.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestOpen(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Toplevel != dir || r.HooksPathConfigured || r.HooksDir != filepath.Join(dir, ".git", "hooks") {
		t.Fatalf("repo = %+v", r)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if r2, err := Open(ctx, sub); err != nil || r2.Toplevel != dir {
		t.Fatalf("from subdir: %+v %v", r2, err)
	}
	if _, err := EnsureConfig(ctx, dir, [][2]string{{"core.hooksPath", ".githooks"}}); err != nil {
		t.Fatal(err)
	}
	r3, err := Open(ctx, dir)
	if err != nil || !r3.HooksPathConfigured || r3.HooksDir != filepath.Join(dir, ".githooks") {
		t.Fatalf("hooksPath not honoured: %+v %v", r3, err)
	}
	if _, err := Open(ctx, t.TempDir()); err != ErrNotRepo {
		t.Fatalf("non-repo: %v", err)
	}
}

func TestEnsureConfigIdempotent(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	kv := [][2]string{{"merge.x.name", "x driver"}, {"merge.x.driver", "x %O %A %B"}}
	changed, err := EnsureConfig(ctx, dir, kv)
	if err != nil || len(changed) != 2 {
		t.Fatalf("first: %v %v", changed, err)
	}
	changed, err = EnsureConfig(ctx, dir, kv)
	if err != nil || len(changed) != 0 {
		t.Fatalf("second must be a no-op: %v %v", changed, err)
	}
	if v, ok, err := ConfigGet(ctx, dir, "merge.x.driver"); err != nil || !ok || v != "x %O %A %B" {
		t.Fatalf("get: %q %v %v", v, ok, err)
	}
	if _, ok, err := ConfigGet(ctx, dir, "merge.none.driver"); err != nil || ok {
		t.Fatalf("unset key: %v %v", ok, err)
	}
}

func TestEnsureBlock(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), ".gitattributes")
	if err := os.WriteFile(p, []byte("*.png binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureBlock(p, "lore", []string{"a merge=x"})
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	if changed, _ := EnsureBlock(p, "lore", []string{"a merge=x"}); changed {
		t.Fatal("second install must be a no-op")
	}
	if changed, _ := EnsureBlock(p, "lore", []string{"a merge=y"}); !changed {
		t.Fatal("changed lines must update the block")
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.HasPrefix(s, "*.png binary\n") || strings.Count(s, ">>> lore >>>") != 1 || !strings.Contains(s, "a merge=y") || strings.Contains(s, "merge=x") {
		t.Fatalf("content = %q", s)
	}
	if !HasBlock(p, "lore") || HasBlock(p, "other") {
		t.Fatal("HasBlock wrong")
	}
}

func TestEnsureHookBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// New script.
	changed, err := EnsureHookBlock(dir, "post-merge", "lore", []string{"echo hi"})
	if err != nil || !changed {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "post-merge"))
	if !strings.HasPrefix(string(b), shebang+"\n# >>> lore >>>") {
		t.Fatalf("new hook = %q", b)
	}
	info, _ := os.Stat(filepath.Join(dir, "post-merge"))
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatal("hook not executable")
	}
	// Existing script that exits early: block must land before the exit.
	existing := "#!/bin/bash\nnpx lint-staged\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "pre-commit"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHookBlock(dir, "pre-commit", "lore", []string{"lore sync hook pre-commit"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "pre-commit"))
	s := string(b)
	if !strings.HasPrefix(s, "#!/bin/bash\n# >>> lore >>>") || !strings.HasSuffix(s, "npx lint-staged\nexit 0\n") {
		t.Fatalf("chained hook = %q", s)
	}
	if changed, _ := EnsureHookBlock(dir, "pre-commit", "lore", []string{"lore sync hook pre-commit"}); changed {
		t.Fatal("re-install must be a no-op")
	}
	// No shebang at all: block is prepended.
	if err := os.WriteFile(filepath.Join(dir, "post-checkout"), []byte("echo user\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHookBlock(dir, "post-checkout", "lore", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "post-checkout"))
	if !strings.HasPrefix(string(b), "# >>> lore >>>") || !strings.HasSuffix(string(b), "echo user\n") {
		t.Fatalf("no-shebang hook = %q", b)
	}
}

func TestUncommittedAndIgnored(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	data := filepath.Join(dir, "d")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Uncommitted(ctx, dir, "d"); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(data, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Uncommitted(ctx, dir, "d")
	if err != nil || len(got) != 1 || got[0] != "d/a.json" {
		t.Fatalf("untracked: %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("d/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ign, err := IsIgnored(ctx, dir, "d/a.json"); err != nil || !ign {
		t.Fatalf("ignored: %v %v", ign, err)
	}
	if ign, err := IsIgnored(ctx, dir, "other.json"); err != nil || ign {
		t.Fatalf("not ignored: %v %v", ign, err)
	}
}

// Review finding: an UNSTAGED modification (" M") as the first entry lost
// its first path character, and paths were repo-root-relative even when the
// caller asked about a subdirectory.
func TestUncommitted_UnstagedAndSubdir(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	sub := filepath.Join(dir, "svc", ".lore", "data")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(sub, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "base")
	// unstaged modification + staged rename
	if err := os.WriteFile(filepath.Join(sub, "a.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "mv", "svc/.lore/data/b.json", "svc/.lore/data/c.json")
	project := filepath.Join(dir, "svc")
	got, err := Uncommitted(ctx, project, ".lore/data")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{".lore/data/a.json": true, ".lore/data/c.json": true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if !want[filepath.ToSlash(p)] {
			t.Fatalf("unexpected path %q in %v", p, got)
		}
		if _, err := os.Stat(filepath.Join(project, p)); err != nil {
			t.Fatalf("path %q does not resolve from the project dir: %v", p, err)
		}
	}
}

// A repo-wide rule can hide files a tool writes; IgnoredPaths names the rule,
// honours negations, and judges tracked paths by the rules alone.
func TestIgnoredPaths(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	if got, err := IgnoredPaths(ctx, dir, nil); err != nil || len(got) != 0 {
		t.Fatalf("no paths: %v %v", got, err)
	}
	if got, err := IgnoredPaths(ctx, dir, []string{"d/_meta.json", "d/x/a.json"}); err != nil || len(got) != 0 {
		t.Fatalf("no rules: %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("_*\n!_keep.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := IgnoredPaths(ctx, dir, []string{"d/_meta.json", "d/x/a.json", "_keep.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["d/_meta.json"] != ".gitignore:1: _*" {
		t.Fatalf("want only d/_meta.json by .gitignore:1: %v", got)
	}
	// A tracked file is still reported: the question is what `git add` does
	// with a new file at that path.
	if err := os.MkdirAll(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "_meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "-f", "d/_meta.json")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -f: %v %s", err, out)
	}
	if got, err := IgnoredPaths(ctx, dir, []string{"d/_meta.json"}); err != nil || len(got) != 1 {
		t.Fatalf("tracked but ignored must be reported: %v %v", got, err)
	}
}

// Add stages new, modified and deleted files under the pathspec only.
func TestAdd(t *testing.T) {
	t.Parallel()
	dir := gitInit(t)
	ctx := context.Background()
	for _, p := range []string{"d/a.json", "d/b.json", "other.txt"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Add(ctx, dir, "d"); err != nil {
		t.Fatal(err)
	}
	staged := func() string {
		cmd := exec.Command("git", "diff", "--cached", "--name-status")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := staged(); got != "A\td/a.json\nA\td/b.json" {
		t.Fatalf("staged %q", got)
	}
	commit := exec.Command("git", "commit", "-qm", "x")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	if err := os.Remove(filepath.Join(dir, "d", "a.json")); err != nil {
		t.Fatal(err)
	}
	if err := Add(ctx, dir, "d"); err != nil {
		t.Fatal(err)
	}
	if got := staged(); got != "D\td/a.json" {
		t.Fatalf("deletion not staged: %q", got)
	}
	if err := Add(ctx, dir, "missing-dir"); err == nil {
		t.Fatal("a pathspec matching nothing must error")
	}
}
