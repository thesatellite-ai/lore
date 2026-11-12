package git

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestPruneRefsKeepsNewest: after a prune only the newest keep refs under
// the prefix stay (by the date of their commit); other refs are untouched.
func TestPruneRefsKeepsNewest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch)
	var refs []string
	for i := 1; i <= 6; i++ {
		date := "2026-0" + strconv.Itoa(i) + "-01T10:00:00Z"
		tree := gitT(t, dir, "write-tree")
		c, err := runner{bin: mustGit(t)}.run(context.Background(), call{dir: dir, args: []string{"commit-tree", tree, "-m", "b" + strconv.Itoa(i)},
			env: []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_COMMITTER_DATE=" + date, "GIT_AUTHOR_DATE=" + date}})
		if err != nil {
			t.Fatal(err)
		}
		ref := compactBackupPrefix + strconv.Itoa(i)
		gitT(t, dir, "update-ref", ref, strings.TrimSpace(string(c)))
		refs = append(refs, ref)
	}
	gitT(t, dir, "update-ref", "refs/lore/live", refs[0]) // another kind: kept
	pruneRefs(context.Background(), runner{bin: mustGit(t)}, dir, compactBackupPrefix, CompactBackupsKept)
	left := strings.Fields(gitT(t, dir, "for-each-ref", "--format=%(refname)", "refs/lore/"))
	want := []string{refs[3], refs[4], refs[5], "refs/lore/live"}
	slices.Sort(want)
	slices.Sort(left)
	if !slices.Equal(left, want) {
		t.Fatalf("refs after the prune = %v, want %v", left, want)
	}
}

// mustGit is the git binary the tests run.
func mustGit(t *testing.T) string {
	t.Helper()
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	return bin
}
