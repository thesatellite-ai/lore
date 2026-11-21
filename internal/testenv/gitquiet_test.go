package testenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGitConfigQuietLeavesNoDetachedMaintenance (devco:D-875): a commit
// under a global gitconfig that starts with GitConfigQuiet starts no
// `git maintenance run --auto --detach` child, the child that outlived a
// test and broke its TempDir cleanup on a loaded machine. Without the
// block git (2.47+) starts one after every commit.
func TestGitConfigQuietLeavesNoDetachedMaintenance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	root := t.TempDir()
	gc := filepath.Join(root, "gitconfig")
	body := GitConfigQuiet + "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(gc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := filepath.Join(root, "repo")
	git := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git(nil, "init", "-q")
	trace := filepath.Join(root, "trace.json")
	git([]string{"GIT_TRACE2_EVENT=" + trace}, "commit", "-q", "--allow-empty", "-m", "one")
	got, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(`"maintenance"`)) {
		t.Fatalf("a commit under GitConfigQuiet started git maintenance (it detaches and outlives the test):\n%s", got)
	}
}
