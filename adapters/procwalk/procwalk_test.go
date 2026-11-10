//go:build linux

package procwalk

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAncestorsFakeProc walks a fake /proc: 10 → 20 (python aider) → 1.
func TestAncestorsFakeProc(t *testing.T) {
	root := t.TempDir()
	mk := func(pid, stat, cmd string) {
		d := filepath.Join(root, pid)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o600)
		_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmd), 0o600)
	}
	mk("10", "10 (ba sh)) S 20 1 1", "/bin/bash\x00")
	mk("20", "20 (python3) S 1 1 1", "/usr/bin/python3\x00/home/u/.local/bin/aider\x00--yes\x00")
	old := procRoot
	procRoot = root
	got := Walker{Start: 10}.Ancestors(8)
	procRoot = old
	if len(got) != 2 || got[1].Exe != "/usr/bin/python3" || got[1].Args[0] != "/home/u/.local/bin/aider" {
		t.Fatalf("got %+v", got)
	}
	if real := New().Ancestors(8); len(real) == 0 {
		t.Fatal("the real walk found no parent")
	}
}
