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
