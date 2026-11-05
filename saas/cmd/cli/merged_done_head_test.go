package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/gitref"
)

// gitIn runs git in dir with a fixed identity and returns trimmed stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := testCmd(t, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestWorkBranchHead_NeverRecordsMergedHEAD (trial finding F17): `task
// done` without --commit records the work branch's HEAD only when that
// commit is not on the fetched default branch yet. A HEAD that is already
// merged (a branch left over from an earlier task) would make the merged
// check report "merged" at once, with a merge time before the done time.
func TestWorkBranchHead_NeverRecordsMergedHEAD(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	clone := filepath.Join(root, "clone")
	gitIn(t, root, "clone", "-q", origin, clone)
	commit := func(name string) string {
		if err := os.WriteFile(filepath.Join(clone, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, clone, "add", name)
		gitIn(t, clone, "commit", "-q", "-m", name)
		return gitIn(t, clone, "rev-parse", "HEAD")
	}
	commit("init")
	gitIn(t, clone, "push", "-q", "-u", "origin", "main")
	gitIn(t, clone, "remote", "set-head", "origin", "main")

	repo, err := gitref.Open(ctx, clone, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Default branch: never a work branch.
	if got := workBranchHead(ctx, repo); got != "" {
		t.Fatalf("on main: got %q, want none", got)
	}

	// A work branch whose HEAD is not merged: recorded.
	gitIn(t, clone, "checkout", "-q", "-b", "agent-a/frontend")
	work := commit("frontend")
	if got := workBranchHead(ctx, repo); got != work {
		t.Fatalf("unmerged work branch: got %q, want %q", got, work)
	}

	// The same branch after it merged into origin/main: HEAD is not this
	// task's work any more, so nothing is recorded (F17).
	gitIn(t, clone, "checkout", "-q", "main")
	gitIn(t, clone, "merge", "-q", "--no-ff", "--no-edit", "agent-a/frontend")
	gitIn(t, clone, "push", "-q", "origin", "main")
	gitIn(t, clone, "checkout", "-q", "agent-a/frontend")
	if got := workBranchHead(ctx, repo); got != "" {
		t.Fatalf("merged work branch HEAD: got %q, want none (done, merge unknown)", got)
	}

	// New work on top of the merged branch is recorded again.
	more := commit("deploy-script")
	if got := workBranchHead(ctx, repo); got != more {
		t.Fatalf("new commit on the branch: got %q, want %q", got, more)
	}

	// No fetched default branch: cannot tell, record nothing.
	gitIn(t, clone, "update-ref", "-d", "refs/remotes/origin/main")
	if got := workBranchHead(ctx, repo); got != "" {
		t.Fatalf("without origin/main: got %q, want none", got)
	}
}
