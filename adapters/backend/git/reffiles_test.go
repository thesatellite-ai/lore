package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRefFromFilesMatchesGit (devco:T-3): a store Get resolved HEAD with a
// git process per row (~20 per imported task). HEAD, a loose branch, a
// packed one and a missing one are read from git's ref files and give what
// rev-parse gives; a layout the files cannot answer for says so.
func TestRefFromFilesMatchesGit(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", testBranch)
	if oid, ok := RefFromFiles(dir, "HEAD"); !ok || oid != "" {
		t.Fatalf("unborn HEAD = %q %v, want \"\" true", oid, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "a.txt")
	gitT(t, dir, "commit", "-q", "-m", "one")
	gitT(t, dir, "branch", "packed")
	gitT(t, dir, "pack-refs", "--all")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "commit", "-q", "-am", "two") // main loose again, packed stays packed
	for _, ref := range []string{"HEAD", "refs/heads/" + testBranch, "refs/heads/packed"} {
		want := strings.TrimSpace(gitT(t, dir, "rev-parse", "--verify", ref+"^{commit}"))
		got, ok := RefFromFiles(dir, ref)
		if !ok || got != want {
			t.Errorf("RefFromFiles(%s) = %q %v, want %q (rev-parse)", ref, got, ok, want)
		}
	}
	if got, ok := RefFromFiles(dir, "refs/heads/none"); !ok || got != "" {
		t.Errorf("a missing branch = %q %v, want \"\" true", got, ok)
	}
	if _, ok := RefFromFiles(dir, "main"); ok {
		t.Error("a short ref name must be left to git")
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, dir, "worktree", "add", "-q", wt, "packed")
	if _, ok := RefFromFiles(wt, "HEAD"); ok {
		t.Error("a linked worktree must be left to git")
	}
	// the store backend's head reads the files: no git process at all
	t.Setenv("PATH", "")
	b := &Backend{work: dir}
	want, _ := RefFromFiles(dir, "HEAD")
	if got, err := b.head(t.Context()); err != nil || got != want {
		t.Errorf("head with no git on PATH = %q %v, want %q", got, err, want)
	}
}
