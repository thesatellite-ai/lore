package wire

// B11 port of saas/cmd/cli/merged_done_head_test.go against the core's
// coordination host (coord_standalone_close.go workBranchHead).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// b11GitEnv is a fixed git identity with no global or system config.
var b11GitEnv = []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
	"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}

// TestWorkBranchHead_NeverRecordsMergedHEAD (trial finding F17): `task
// done` without --commit records the work branch's HEAD only when that
// commit is not on the fetched default branch yet. A HEAD that is already
// merged (a branch left over from an earlier task) would make the merged
// check report "merged" at once, with a merge time before the done time.
func TestWorkBranchHead_NeverRecordsMergedHEAD(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, b11GitEnv, "init", "-q", "--bare", "-b", "main", origin)
	clone := filepath.Join(root, "clone")
	gitIn(t, root, b11GitEnv, "clone", "-q", origin, clone)
	commit := func(name string) string {
		if err := os.WriteFile(filepath.Join(clone, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, clone, b11GitEnv, "add", name)
		gitIn(t, clone, b11GitEnv, "commit", "-q", "-m", name)
		return strings.TrimSpace(gitIn(t, clone, b11GitEnv, "rev-parse", "HEAD"))
	}
	commit("init")
	gitIn(t, clone, b11GitEnv, "push", "-q", "-u", "origin", "main")
	gitIn(t, clone, b11GitEnv, "remote", "set-head", "origin", "main")
	repo, err := gitref.Open(ctx, clone, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &coordWorld{repo: repo, remoteName: "origin"}
	// Default branch: never a work branch.
	if got := w.workBranchHead(ctx); got != "" {
		t.Fatalf("on main: got %q, want none", got)
	}
	// A work branch whose HEAD is not merged: recorded.
	gitIn(t, clone, b11GitEnv, "checkout", "-q", "-b", "agent-a/frontend")
	work := commit("frontend")
	if got := w.workBranchHead(ctx); got != work {
		t.Fatalf("unmerged work branch: got %q, want %q", got, work)
	}
	// The same branch after it merged into origin/main: HEAD is not this
	// task's work any more, so nothing is recorded (F17).
	gitIn(t, clone, b11GitEnv, "checkout", "-q", "main")
	gitIn(t, clone, b11GitEnv, "merge", "-q", "--no-ff", "--no-edit", "agent-a/frontend")
	gitIn(t, clone, b11GitEnv, "push", "-q", "origin", "main")
	gitIn(t, clone, b11GitEnv, "checkout", "-q", "agent-a/frontend")
	if got := w.workBranchHead(ctx); got != "" {
		t.Fatalf("merged work branch HEAD: got %q, want none (done, merge unknown)", got)
	}
	// New work on top of the merged branch is recorded again.
	more := commit("deploy-script")
	if got := w.workBranchHead(ctx); got != more {
		t.Fatalf("new commit on the branch: got %q, want %q", got, more)
	}
	// No fetched default branch: cannot tell, record nothing.
	gitIn(t, clone, b11GitEnv, "update-ref", "-d", "refs/remotes/origin/main")
	if got := w.workBranchHead(ctx); got != "" {
		t.Fatalf("without origin/main: got %q, want none", got)
	}
}
