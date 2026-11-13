package host

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestHoldsBranch: A-15, the "it may have landed on main while this
// branch is older" hint is only true when HEAD lacks the default branch's
// fetched tip; holdsBranch tells the two apart.
func TestHoldsBranch(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")
	ctx := context.Background()
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if holdsBranch(ctx, repo, "origin", "main") {
		t.Fatal("no tracking ref: must keep the older-branch hint")
	}
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	if !holdsBranch(ctx, repo, "origin", "main") {
		t.Fatal("HEAD at origin/main: the branch is not older")
	}
	git("commit", "-q", "--allow-empty", "-m", "two")
	if !holdsBranch(ctx, repo, "origin", "main") {
		t.Fatal("HEAD ahead of origin/main: the branch is not older")
	}
	git("checkout", "-q", "-b", "old", "HEAD~1")
	git("update-ref", "refs/remotes/origin/main", "main")
	if holdsBranch(ctx, repo, "origin", "main") {
		t.Fatal("HEAD behind origin/main: the branch is older")
	}
}
