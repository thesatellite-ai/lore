package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEnsureGitignoreHidesBrokenDBCopies pins that the copy restore/repair
// keeps of a replaced DB (lore.db.broken.<stamp>, every private row) is
// git-ignored, alongside the live DB files.
func TestEnsureGitignoreHidesBrokenDBCopies(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for this test")
	}
	root := t.TempDir()
	if out, err := testCmd(t, "git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	ctx := context.Background()
	if err := ensureGitignore(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitignore(ctx, root); err != nil { // run twice: no duplicate lines
		t.Fatal(err)
	}
	for _, p := range []string{
		".lore/lore.db",
		".lore/lore.db-wal",
		".lore/lore.db.broken." + "20260101-000000",
	} {
		if err := testCmd(t, "git", "-C", root, "check-ignore", "-q", p).Run(); err != nil {
			t.Errorf("%s is not ignored after ensureGitignore", p)
		}
	}
	if err := testCmd(t, "git", "-C", root, "check-ignore", "-q", ".lore/data/tasks/x.json").Run(); err == nil {
		t.Error(".lore/data must stay tracked")
	}
	b, err := os.ReadFile(filepath.Clean(filepath.Join(root, ".gitignore")))
	if err != nil {
		t.Fatal(err)
	}
	if n := countLines(string(b), ".lore/lore.db.broken.*"); n != 1 {
		t.Errorf("broken-copy pattern written %d times, want 1:\n%s", n, b)
	}
}

func countLines(s, line string) int {
	n := 0
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			if s[start:i] == line {
				n++
			}
			start = i + 1
		}
	}
	return n
}
