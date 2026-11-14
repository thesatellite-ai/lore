package wire

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestCommitsPresentOneBatch (D-110): doctor's stale commit-link check
// asks git about every off-branch commit in one cat-file --batch-check
// (it ran one git process per link: 177 in a 5,500-row repo). A commit
// the repo holds is present; an unknown sha and a tree are not.
func TestCommitsPresentOneBatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.test"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "one")
	commit := git("rev-parse", "HEAD")
	tree := git("rev-parse", "HEAD^{tree}")
	unknown := strings.Repeat("ab", 20)
	have := commitsPresent(context.Background(), dir, []string{commit, unknown, tree})
	if !have[commit] || have[unknown] || have[tree] || len(have) != 1 {
		t.Fatalf("present = %v, want only %s", have, commit)
	}
	if got := commitsPresent(context.Background(), dir, nil); len(got) != 0 {
		t.Fatalf("no shas: %v", got)
	}
}
