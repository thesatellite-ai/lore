package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOrgLinkReadsRunGitOncePerTip (devco:T-3): every write resolved the
// org's links with ~60 git processes (branch tips, tree listings, each
// folder's _meta.json), the same answers for each of 650 imported rows.
// A branch tip is now kept with what its refs hold and a read of a fixed
// commit is kept outright: the second time asks no git (none is on PATH),
// and a branch that moved is read again.
func TestOrgLinkReadsRunGitOncePerTip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q", "-b", "main")
	commit := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "lore-store.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		testGit(t, dir, "add", "lore-store.yaml")
		testGit(t, dir, "commit", "-q", "-m", body)
	}
	commit("one")
	first, ok := branchTip(ctx, dir, "main")
	if !ok {
		t.Fatal("no tip for main")
	}
	commit("two")
	moved, ok := branchTip(ctx, dir, "main")
	if !ok || moved == first {
		t.Fatalf("after a commit the tip is %q, want a new one (was %q)", moved, first)
	}
	listing, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", moved)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "") // from here on a git process cannot start
	again, ok := branchTip(ctx, dir, "main")
	if !ok || again != moved {
		t.Errorf("an unchanged branch's tip with no git = %q %v, want %q (kept)", again, ok, moved)
	}
	kept, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", moved)
	if err != nil || kept != listing {
		t.Errorf("a fixed commit's listing with no git = %q %v, want it kept", kept, err)
	}
	if _, err := gitRun(ctx, "-C", dir, "ls-tree", "-r", "-z", "--name-only", "main"); err == nil {
		t.Error("a listing by branch name was answered with no git: only a full commit id names fixed content")
	}
}
