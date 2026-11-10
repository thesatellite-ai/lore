package reposync

// Ports of saas/pkg/aicoder/gitsetup merge_test.go (later 623) onto the
// merge helpers that moved here (git.go): the merge probe, a merge with a
// per-invocation driver, the clean check, git's version and the recorded
// default branch.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// divergedRepo is a repo on "feature" whose a.txt and b.txt were edited on
// both "feature" and "main" since they forked (c.txt only on main).
func divergedRepo(t *testing.T) string {
	t.Helper()
	dir := gitInitRepo(t)
	git := func(args ...string) { t.Helper(); gitT(t, dir, args...) }
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeRepoFile(t, dir, f, "base\n")
	}
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "a.txt", "feature\n")
	writeRepoFile(t, dir, "b.txt", "feature\n")
	git("commit", "-qam", "feature")
	git("checkout", "-q", "main")
	writeRepoFile(t, dir, "a.txt", "main\n")
	writeRepoFile(t, dir, "b.txt", "main\n")
	writeRepoFile(t, dir, "c.txt", "main only\n")
	git("commit", "-qam", "main")
	git("checkout", "-q", "feature")
	return dir
}

// TestProbeMerge: conflicts are named without moving HEAD or touching the
// work tree; a clean merge and an up-to-date branch are told apart; an
// unknown ref is an error.
func TestProbeMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := divergedRepo(t)
	head := gitT(t, dir, "rev-parse", "HEAD")
	p, err := probeMerge(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if p.upToDate || strings.Join(p.conflicted, ",") != "a.txt,b.txt" {
		t.Fatalf("probe = %+v", p)
	}
	if gitT(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("probing moved HEAD")
	}
	if clean, cerr := isClean(ctx, dir); cerr != nil || !clean {
		t.Fatalf("probing touched the work tree: %v %v", clean, cerr)
	}
	gitT(t, dir, "checkout", "-q", "-b", "other", "main~1")
	writeRepoFile(t, dir, "d.txt", "new\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "other")
	if cp, cerr := probeMerge(ctx, dir, "main"); cerr != nil || cp.upToDate || len(cp.conflicted) != 0 {
		t.Fatalf("clean probe = %+v %v", cp, cerr)
	}
	gitT(t, dir, "merge", "-q", "--no-edit", "main")
	if up, uerr := probeMerge(ctx, dir, "main"); uerr != nil || !up.upToDate {
		t.Fatalf("up-to-date probe = %+v %v", up, uerr)
	}
	if _, nerr := probeMerge(ctx, dir, "no-such-ref"); nerr == nil {
		t.Fatal("an unknown ref must be an error")
	}
}

// TestProbeMergeIgnoresConfiguredDrivers: a clone with a merge driver
// configured is still told what a host without the driver sees (its web
// merge button reports the conflict the driver would settle).
func TestProbeMergeIgnoresConfiguredDrivers(t *testing.T) {
	t.Parallel()
	dir := divergedRepo(t)
	writeRepoFile(t, dir, ".gitattributes", "*.txt merge=pick\n")
	gitT(t, dir, "add", ".gitattributes")
	gitT(t, dir, "commit", "-qm", "attributes")
	gitT(t, dir, "config", "merge.pick.driver", "true") // would settle every conflict
	p, err := probeMerge(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.conflicted, ",") != "a.txt,b.txt" {
		t.Fatalf("the probe used the configured driver: %+v", p)
	}
}

// TestMergeNoCommitWithDriver: a failing per-invocation driver leaves the
// files unmerged and abortMerge restores the branch; a settling driver
// lets the merge commit (two parents, main's other change kept) and the
// driver never reaches the repo's config.
func TestMergeNoCommitWithDriver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := divergedRepo(t)
	writeRepoFile(t, dir, ".gitattributes", "*.txt merge=pick\n")
	gitT(t, dir, "add", ".gitattributes")
	gitT(t, dir, "commit", "-qm", "attributes")
	head := gitT(t, dir, "rev-parse", "HEAD")

	if err := mergeNoCommit(ctx, dir, "main", [][2]string{{"merge.pick.driver", "false"}}); err != nil {
		t.Fatal(err)
	}
	left, err := unmerged(ctx, dir)
	if err != nil || strings.Join(left, ",") != "a.txt,b.txt" {
		t.Fatalf("unmerged = %v %v", left, err)
	}
	if aerr := abortMerge(ctx, dir); aerr != nil {
		t.Fatal(aerr)
	}
	if clean, _ := isClean(ctx, dir); !clean || gitT(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("abort did not restore the branch")
	}

	if merr := mergeNoCommit(ctx, dir, "main", [][2]string{{"merge.pick.driver", "true"}}); merr != nil {
		t.Fatal(merr)
	}
	if rest, _ := unmerged(ctx, dir); len(rest) != 0 {
		t.Fatalf("the driver did not settle: %v", rest)
	}
	commit, err := commitMerge(ctx, dir, "merge main")
	if err != nil || commit == head {
		t.Fatalf("commit = %q %v", commit, err)
	}
	if parents := strings.Fields(gitT(t, dir, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Fatalf("not a merge commit: %v", parents)
	}
	if got, _ := os.ReadFile(filepath.Clean(filepath.Join(dir, "c.txt"))); string(got) != "main only\n" {
		t.Fatalf("main's non-conflicting change lost: %q", got)
	}
	if _, cerr := run(ctx, dir, "config", "--get", "merge.pick.driver"); cerr == nil {
		t.Fatal("the per-invocation driver leaked into the repo config")
	}
}

// TestIsClean: an empty repo is clean; an untracked file is not.
func TestIsClean(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitInitRepo(t)
	if clean, err := isClean(ctx, dir); err != nil || !clean {
		t.Fatalf("empty repo: %v %v", clean, err)
	}
	writeRepoFile(t, dir, "x", "1")
	if clean, _ := isClean(ctx, dir); clean {
		t.Fatal("an untracked file must count as not clean")
	}
}

// TestParseGitVersion: release, vendor and Windows spellings; garbage is
// refused rather than read as 0.0.
func TestParseGitVersion(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][2]int{
		"git version 2.50.1 (Apple Git-155)": {2, 50},
		"git version 2.38.0":                 {2, 38},
		"git version 2.43.0.windows.1":       {2, 43},
	} {
		major, minor, ok := parseGitVersion(in)
		if !ok || major != want[0] || minor != want[1] {
			t.Fatalf("%q -> %d.%d %v", in, major, minor, ok)
		}
	}
	for _, bad := range []string{"", "git version", "git version x.y", "git version 2"} {
		if _, _, ok := parseGitVersion(bad); ok {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

// TestDefaultBranch: a clone records its remote's default branch (trunk
// here, not main); a repo without that remote has none.
func TestDefaultBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	origin := gitInitRepo(t)
	gitT(t, origin, "checkout", "-q", "-b", "trunk")
	gitT(t, origin, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	if b, ok := defaultBranch(ctx, clone, "origin"); !ok || b != "trunk" {
		t.Fatalf("default branch = %q %v", b, ok)
	}
	if _, ok := defaultBranch(ctx, origin, "origin"); ok {
		t.Fatal("a repo without that remote has no recorded default")
	}
}
