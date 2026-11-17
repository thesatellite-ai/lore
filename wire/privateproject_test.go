package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLoreDirIgnoredMemoSkipsGit: the hooks of one commit asked `git
// check-ignore` on every wiring pass, a sixth git process over the budget
// of five (devco D-511). The memo answers from its key while no ignore
// file changed (git is not even on PATH the second time) and asks git
// again once one does.
func TestLoreDirIgnoredMemoSkipsGit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, loreDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	ignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignore, []byte(".lore/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	memo := filepath.Join(root, ".git", "lore", "lore-dir-ignored")
	inputs := []string{ignore, filepath.Join(root, ".git", "info", "exclude")}
	if !LoreDirIgnoredMemo(ctx, root, memo, inputs) {
		t.Fatal("first ask: .lore/ is ignored, memo said not")
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // no git: only the memo can answer
	if !LoreDirIgnoredMemo(ctx, root, memo, inputs) {
		t.Fatal("second ask: the memo did not answer; it asked git again")
	}
	if LoreDirIgnored(ctx, root) {
		t.Fatal("without git the plain check must read as not ignored (test premise)")
	}
	t.Setenv("PATH", path)
	if err := os.WriteFile(ignore, []byte("build/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(ignore, later, later); err != nil {
		t.Fatal(err)
	}
	if LoreDirIgnoredMemo(ctx, root, memo, inputs) {
		t.Fatal("after .gitignore stopped listing .lore/ the memo still said ignored")
	}
}
