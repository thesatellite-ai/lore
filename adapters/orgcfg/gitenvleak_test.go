package orgcfg

// Issue #6: `task check` runs from the pre-push hook, where git exports
// GIT_DIR (and GIT_WORK_TREE in some setups). Child git that inherited them
// ignored -C and acted on the developer's own clone: config turned bare,
// origin rewritten to a temp folder, junk remotes, a shallow file. This
// test runs orgcfg's git paths (init --bare, clone, commit, push, remote
// set-url, fetch) with both variables pointing at a decoy repository and
// asserts the decoy is untouched.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// decoyState is what issue #6 saw damaged in the hook's repository.
type decoyState struct {
	config   string // .git/config: bare flag, remotes, [user]
	shallow  bool   // .git/shallow exists
	worktree bool   // .git/worktrees exists
}

func readDecoy(t *testing.T, gitDir string) decoyState {
	t.Helper()
	cfg, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	_, shErr := os.Stat(filepath.Join(gitDir, "shallow"))
	_, wtErr := os.Stat(filepath.Join(gitDir, "worktrees"))
	return decoyState{config: string(cfg), shallow: shErr == nil, worktree: wtErr == nil}
}

// pointGitAtDecoy makes a decoy work tree and sets GIT_DIR (and, with
// withWorkTree, GIT_WORK_TREE) to it for the rest of the test, as a
// pre-push hook does; the returned func fails the test when the decoy
// changed.
func pointGitAtDecoy(t *testing.T, withWorkTree bool) func() {
	t.Helper()
	work := t.TempDir()
	cmd := exec.Command("git", "init", "-q", work)
	cmd.Env = gitenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init decoy: %v %s", err, out)
	}
	gitDir := filepath.Join(work, ".git")
	before := readDecoy(t, gitDir)
	t.Setenv("GIT_DIR", gitDir)
	if withWorkTree {
		t.Setenv("GIT_WORK_TREE", work)
	}
	return func() {
		t.Helper()
		if after := readDecoy(t, gitDir); after != before {
			t.Fatalf("the decoy repository named by GIT_DIR changed:\nbefore %+v\nafter  %+v", before, after)
		}
	}
}

func TestGitIgnoresInheritedGitDir(t *testing.T) {
	for _, withWorkTree := range []bool{false, true} {
		name := "GIT_DIR"
		if withWorkTree {
			name += "+GIT_WORK_TREE"
		}
		t.Run(name, func(t *testing.T) { runOrgGitAgainstDecoy(t, withWorkTree) })
	}
}

func runOrgGitAgainstDecoy(t *testing.T, withWorkTree bool) {
	check := pointGitAtDecoy(t, withWorkTree)
	ctx := context.Background()
	origin := storeOrigin(t) // init --bare, clone, commit, push, all with -C
	root := t.TempDir()
	dir := filepath.Join(root, "clone.git")
	if err := ensureBare(ctx, dir, filepath.Join(root, "moved.git")); err != nil {
		t.Fatal(err)
	}
	if err := ensureBare(ctx, dir, origin); err != nil { // remote set-url
		t.Fatal(err)
	}
	if err := fetch(ctx, nil, dir); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "config", "--get", "remote."+remoteName+".url"); got != origin {
		t.Fatalf("clone origin = %q, want %q", got, origin)
	}
	if got := testGit(t, dir, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Fatalf("clone is not bare: %q", got)
	}
	check()
}
