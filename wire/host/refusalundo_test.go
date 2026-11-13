package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSetupSnapshotRestore (B1-1): the set-up a refused write made first
// (.gitignore lines, a new hook, a changed .git/config) is put back as it
// was; a file that did not exist is removed, an existing one gets its bytes.
func TestSetupSnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	ignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(ignore, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ".git", "config")
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	snap := takeSetupSnapshot(context.Background(), dir)
	if snap == nil {
		t.Fatal("no snapshot in a git repo")
	}
	hook := filepath.Join(dir, ".git", "hooks", hookPreCommit)
	for p, b := range map[string]string{ignore: "node_modules/\n# lore\n.lore/lock\n", hook: "#!/bin/sh\nlore sync hook pre-commit\n", cfg: string(before) + "[merge \"lore\"]\n\tname = lore\n"} {
		if err := os.WriteFile(p, []byte(b), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if undone := snap.restore(); len(undone) != 3 {
		t.Fatalf("undone %v, want .gitignore, the hook and .git/config", undone)
	}
	if b, _ := os.ReadFile(ignore); string(b) != "node_modules/\n" {
		t.Fatalf(".gitignore not restored: %q", b)
	}
	if _, err := os.Stat(hook); err == nil {
		t.Fatal("the new hook was left")
	}
	if b, _ := os.ReadFile(cfg); string(b) != string(before) {
		t.Fatalf(".git/config not restored: %q", b)
	}
	if again := snap.restore(); len(again) != 0 {
		t.Fatalf("a second restore changed %v", again)
	}
}

// TestTreeSnapshotRestore (W-6): a refused write's earlier changes to the
// work tree (a rewritten tracked file, a new file) go back; a change the
// person had before stays.
func TestTreeSnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	write := func(p, b string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("row.json", "v1\n")
	write("mine.txt", "base\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	write("mine.txt", "my edit\n") // the person's own change, before the write
	snap := takeTreeSnapshot(context.Background(), dir)
	if snap == nil {
		t.Fatal("no snapshot")
	}
	write("row.json", "v2\n")         // the auto-migrate's rewrite
	write("LORE.full.md", "render\n") // a new rendered file
	write("mine.txt", "my edit\nmore\n")
	snap.restore(context.Background())
	for p, want := range map[string]string{"row.json": "v1\n", "mine.txt": "my edit\n"} {
		if b, _ := os.ReadFile(filepath.Join(dir, p)); string(b) != want {
			t.Errorf("%s = %q, want %q", p, b, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "LORE.full.md")); err == nil {
		t.Error("the new file was left")
	}
}
