package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/live"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// F6 skip list: no ride commit on a detached HEAD, on the default branch, or
// while a rebase / cherry-pick / revert / merge / bisect is in progress.
func TestRideBranchSkipList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	repo, err := gitref.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := func() *liveSession { return &liveSession{repo: repo, ref: &live.Ref{Repo: repo, RemoteName: "origin"}} }
	ctx := context.Background()
	if _, ok := sess().rideBranch(ctx); ok {
		t.Fatal("no ride on the default branch")
	}
	gitT(t, dir, "checkout", "-q", "-b", "feat/x")
	if b, ok := sess().rideBranch(ctx); !ok || b != "feat/x" {
		t.Fatalf("a feature branch rides: %q %v", b, ok)
	}
	gitDir := filepath.Join(dir, ".git")
	for _, m := range rideInProgressMarkers {
		p := filepath.Join(gitDir, m)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := sess().rideBranch(ctx); ok {
			t.Fatalf("no ride while %s exists", m)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "checkout", "-q", "--detach")
	if _, ok := sess().rideBranch(ctx); ok {
		t.Fatal("no ride on a detached HEAD")
	}
}
