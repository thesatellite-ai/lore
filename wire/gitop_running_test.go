package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitOpRunningRealRebase is devco:Q-15: a real rebase stopped on a
// conflict is named, with the command that ends it; once it is aborted,
// nothing is running.
func TestGitOpRunningRealRebase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=T", "-c", "user.email=t@example.test", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
		_, err := cmd.CombinedOutput()
		return err
	}
	must := func(args ...string) {
		t.Helper()
		if err := git(args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must("init", "-q")
	write("base\n")
	must("add", "-A")
	must("commit", "-qm", "base")
	must("checkout", "-qb", "topic")
	write("topic\n")
	must("commit", "-qam", "topic")
	must("checkout", "-q", "main")
	write("main\n")
	must("commit", "-qam", "main")
	must("checkout", "-q", "topic")
	if _, ok := GitOpRunning(ctx, dir); ok {
		t.Fatal("clean work tree: GitOpRunning says an operation is running")
	}
	if err := git("rebase", "main"); err == nil {
		t.Fatal("the rebase did not stop on the conflict")
	}
	op, ok := GitOpRunning(ctx, dir)
	if !ok || op.Name != "rebase" || !strings.Contains(op.End, "git rebase --continue") {
		t.Fatalf("stopped rebase: GitOpRunning = %+v, %v; want rebase with git rebase --continue", op, ok)
	}
	must("rebase", "--abort")
	if op, ok := GitOpRunning(ctx, dir); ok {
		t.Fatalf("after rebase --abort: GitOpRunning = %+v", op)
	}
	if _, ok := GitOpRunning(ctx, t.TempDir()); ok {
		t.Fatal("not a git checkout: GitOpRunning says an operation is running")
	}
}
