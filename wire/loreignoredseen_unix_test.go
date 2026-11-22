//go:build unix

package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitShim puts a git on PATH that logs every run to the returned file and
// then runs the real git. Not parallel: it sets PATH for the process.
func gitShim(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil { //nolint:gosec // test shim must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// checkIgnoreRuns counts the `check-ignore` runs in the shim's log.
func checkIgnoreRuns(t *testing.T, log string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(log))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(b), "check-ignore")
}

// devco:D-882: the callers of one command (render's pointer and stamp,
// doctor's sync and .gitignore checks) share one `git check-ignore` for
// .lore/; a change to an ignore file asks git again, so the answer is
// never stale (lore adds .gitignore lines between two calls).
func TestLoreDirIgnoredAsksGitOncePerIgnoreState(t *testing.T) {
	ctx := context.Background()
	root := privateRepo(t, "node_modules/\n")
	log := gitShim(t)
	for i := range 3 {
		if !LoreDirIgnored(ctx, root) {
			t.Fatalf("call %d: .lore/ in info/exclude reads as not ignored", i)
		}
	}
	if n := checkIgnoreRuns(t, log); n != 1 {
		t.Fatalf("three asks in one process started %d git check-ignore, want 1", n)
	}
	// the exclude emptied (another size): git is asked again and says no
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// two asks after one change: the first runs git, the second is served
	// from the cache (two calls, not one expression twice, so the count
	// below checks the cache and staticcheck does not read it as a typo)
	first := LoreDirIgnored(ctx, root)
	second := LoreDirIgnored(ctx, root)
	if first || second {
		t.Fatalf("after the exclude was emptied .lore/ still reads as ignored (first ask %v, second %v)", first, second)
	}
	if n := checkIgnoreRuns(t, log); n != 2 {
		t.Fatalf("after one ignore change git check-ignore ran %d times in all, want 2", n)
	}
	// a .gitignore line ignoring it: asked again, ignored
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n.lore/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !LoreDirIgnored(ctx, root) {
		t.Fatal("a .gitignore line .lore/ reads as not ignored")
	}
	if n := checkIgnoreRuns(t, log); n != 3 {
		t.Fatalf("after a .gitignore change git check-ignore ran %d times in all, want 3", n)
	}
}

// devco:D-882: a worktree's .git file leads to the main clone's
// info/exclude, so a change there is seen from the worktree too.
func TestLocalIgnoreInputsFollowWorktree(t *testing.T) {
	t.Parallel()
	main := t.TempDir()
	gd := filepath.Join(main, ".git", "worktrees", "wt")
	if err := os.MkdirAll(gd, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gd+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(main, ".git", "info", "exclude")
	for _, p := range localIgnoreInputs(filepath.Join(wt, "sub")) {
		if p == want {
			return
		}
	}
	t.Fatalf("inputs of a worktree miss the main clone's %s: %v", want, localIgnoreInputs(filepath.Join(wt, "sub")))
}
