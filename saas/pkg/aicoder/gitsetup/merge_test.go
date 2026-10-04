package gitsetup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs git in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// divergedRepo returns a repo on branch "feature" whose a.txt and b.txt were
// edited on both "feature" and "main" since they forked.
func divergedRepo(t *testing.T) string {
	t.Helper()
	dir := gitInit(t)
	gitRun(t, dir, "checkout", "-q", "-b", "main")
	writeFile(t, dir, "a.txt", "base\n")
	writeFile(t, dir, "b.txt", "base\n")
	writeFile(t, dir, "c.txt", "base\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-qm", "base")
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "a.txt", "feature\n")
	writeFile(t, dir, "b.txt", "feature\n")
	gitRun(t, dir, "commit", "-qam", "feature")
	gitRun(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "a.txt", "main\n")
	writeFile(t, dir, "b.txt", "main\n")
	writeFile(t, dir, "c.txt", "main only\n")
	gitRun(t, dir, "commit", "-qam", "main")
	gitRun(t, dir, "checkout", "-q", "feature")
	return dir
}

func TestProbeMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := divergedRepo(t)
	head := gitRun(t, dir, "rev-parse", "HEAD")
	p, err := ProbeMerge(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if p.UpToDate || strings.Join(p.Conflicted, ",") != "a.txt,b.txt" {
		t.Fatalf("probe = %+v", p)
	}
	if gitRun(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("probing moved HEAD")
	}
	if clean, err := IsClean(ctx, dir); err != nil || !clean {
		t.Fatalf("probing touched the work tree: %v %v", clean, err)
	}
	// Clean merge: a branch that only touched c.txt's neighbour.
	gitRun(t, dir, "checkout", "-q", "-b", "other", "main~1")
	writeFile(t, dir, "d.txt", "new\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-qm", "other")
	if p, err := ProbeMerge(ctx, dir, "main"); err != nil || p.UpToDate || len(p.Conflicted) != 0 {
		t.Fatalf("clean probe = %+v %v", p, err)
	}
	// Up to date: main already contained.
	gitRun(t, dir, "merge", "-q", "--no-edit", "main")
	if p, err := ProbeMerge(ctx, dir, "main"); err != nil || !p.UpToDate {
		t.Fatalf("up-to-date probe = %+v %v", p, err)
	}
	if _, err := ProbeMerge(ctx, dir, "no-such-ref"); err == nil {
		t.Fatal("unknown ref must error")
	}
}

// A driver supplied per invocation settles the conflicts, leaves the repo's
// config untouched, and the merge commits; a driver that fails leaves files
// unmerged and AbortMerge restores the branch.
func TestMergeNoCommitWithDriver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := divergedRepo(t)
	writeFile(t, dir, ".gitattributes", "*.txt merge=pick\n")
	gitRun(t, dir, "add", ".gitattributes")
	gitRun(t, dir, "commit", "-qm", "attributes")
	head := gitRun(t, dir, "rev-parse", "HEAD")

	failing := [][2]string{{"merge.pick.driver", "false"}}
	if err := MergeNoCommit(ctx, dir, "main", failing); err != nil {
		t.Fatal(err)
	}
	left, err := Unmerged(ctx, dir)
	if err != nil || strings.Join(left, ",") != "a.txt,b.txt" {
		t.Fatalf("unmerged = %v %v", left, err)
	}
	if err := AbortMerge(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if clean, _ := IsClean(ctx, dir); !clean || gitRun(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("abort did not restore the branch")
	}

	// "true" keeps ours (%A is left as is) and reports success.
	ok := [][2]string{{"merge.pick.driver", "true"}}
	if err := MergeNoCommit(ctx, dir, "main", ok); err != nil {
		t.Fatal(err)
	}
	if left, _ := Unmerged(ctx, dir); len(left) != 0 {
		t.Fatalf("driver did not settle: %v", left)
	}
	commit, err := CommitMerge(ctx, dir, "merge main")
	if err != nil || commit == head {
		t.Fatalf("commit = %q %v", commit, err)
	}
	if parents := strings.Fields(gitRun(t, dir, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Fatalf("not a merge commit: %v", parents)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "c.txt")); string(got) != "main only\n" {
		t.Fatalf("non-conflicting change from main lost: %q", got)
	}
	if v, ok, _ := ConfigGet(ctx, dir, "merge.pick.driver"); ok {
		t.Fatalf("per-invocation driver leaked into the repo config: %q", v)
	}
}

func TestIsClean(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitInit(t)
	if clean, err := IsClean(ctx, dir); err != nil || !clean {
		t.Fatalf("empty repo: %v %v", clean, err)
	}
	writeFile(t, dir, "x", "1")
	if clean, _ := IsClean(ctx, dir); clean {
		t.Fatal("untracked file must count as not clean")
	}
}

func TestParseGitVersion(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][2]int{
		"git version 2.50.1 (Apple Git-155)": {2, 50},
		"git version 2.38.0":                 {2, 38},
		"git version 2.43.0.windows.1":       {2, 43},
	} {
		major, minor, ok := parseGitVersion(in)
		if !ok || major != want[0] || minor != want[1] {
			t.Fatalf("%q → %d.%d %v", in, major, minor, ok)
		}
	}
	for _, bad := range []string{"", "git version", "git version x.y"} {
		if _, _, ok := parseGitVersion(bad); ok {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

func TestDefaultBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	origin := gitInit(t)
	gitRun(t, origin, "checkout", "-q", "-b", "trunk")
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	if b, ok := DefaultBranch(ctx, clone, "origin"); !ok || b != "trunk" {
		t.Fatalf("default branch = %q %v", b, ok)
	}
	if _, ok := DefaultBranch(ctx, origin, "origin"); ok {
		t.Fatal("a repo without that remote has no recorded default")
	}
}

// A clone that has a merge driver configured must still be told what a host
// without that driver sees: merge-tree would otherwise run the driver and
// report a clean merge where the host's merge button reports a conflict.
func TestProbeMergeIgnoresConfiguredDrivers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := divergedRepo(t)
	writeFile(t, dir, ".gitattributes", "*.txt merge=pick\n")
	gitRun(t, dir, "add", ".gitattributes")
	gitRun(t, dir, "commit", "-qm", "attributes")
	gitRun(t, dir, "config", "merge.pick.driver", "true") // would settle every conflict
	p, err := ProbeMerge(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Conflicted, ",") != "a.txt,b.txt" {
		t.Fatalf("probe used the configured driver: %+v", p)
	}
}
