package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// TestLocalGitConfigFileWorktree pins D-5: in a linked worktree the message
// names the main clone's config (what `git config --local` changed), never
// <worktree>/.git/config, which is a path under a one-line gitdir file.
func TestLocalGitConfigFileWorktree(t *testing.T) {
	root := t.TempDir()
	main, wt := filepath.Join(root, "app"), filepath.Join(root, "app-wt")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitIn(t, main)
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.test", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", "--detach", wt)
	ctx := context.Background()
	want, _ := filepath.EvalSymlinks(filepath.Join(main, ".git", "config"))
	for _, dir := range []string{main, wt} {
		got, _ := filepath.EvalSymlinks(localGitConfigFile(ctx, dir))
		if got != want {
			t.Fatalf("localGitConfigFile(%s) = %q, want %q", dir, got, want)
		}
	}
}
