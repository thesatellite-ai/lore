package sqlite

// walorphan_test.go — a cache file deleted by hand while another lore
// process (`lore api`, a TUI) has it open. The other process is a real one
// (this test binary run again as a holder): SQLite's locks are per
// process, and within one process the case does not arise.

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// envHoldCache names the cache file the holder process opens.
const envHoldCache = "LORE_TEST_HOLD_CACHE"

// holderReady is the line the holder prints once its write is committed.
const holderReady = "holder ready"

// TestHelperHoldCache is the holder: it opens the cache, commits a row
// (so the -wal and -shm exist), says so and keeps the file open until its
// stdin closes. It does nothing in an ordinary run.
func TestHelperHoldCache(t *testing.T) {
	path := os.Getenv(envHoldCache)
	if path == "" {
		return
	}
	f := newFixture(t)
	c, err := Open(f.ctx, path, f.reg, f.exp)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	f.apply(c, "repo", "c1", f.memory(newMem(), newPrj(), "held"))
	if _, err := os.Stdout.WriteString(holderReady + "\n"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin) // hold the file until the test closes stdin
}

// TestOpenAfterDeleteWhileOpen: the cache file is deleted while another
// process holds it open; that process's -wal and -shm stay behind. The
// next Open must build a fresh cache instead of replaying the orphaned
// WAL into the new file (SQLITE_IOERR_SHORT_READ: E_INTERNAL "sqlite
// cache: open" on every command until the other process exited).
func TestOpenAfterDeleteWhileOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to delete a file another process holds open, so a cache deleted under a running lore cannot happen there")
	}
	f := newFixture(t)
	path := filepath.Join(f.dir, "c.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldCache$", "-test.count=1")
	cmd.Env = append(os.Environ(), envHoldCache+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	sc := bufio.NewScanner(stdout)
	ready := false
	for !ready && sc.Scan() {
		ready = strings.Contains(sc.Text(), holderReady)
	}
	if !ready {
		t.Fatalf("the holder process never opened the cache")
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	if !fileExists(path + "-wal") {
		t.Fatalf("no -wal beside the held cache: the test does not reproduce the case")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	c, err := Open(f.ctx, path, f.reg, f.exp)
	if err != nil {
		t.Fatalf("open after the file was deleted under another process: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	f.apply(c, "repo", "c1", f.memory(newMem(), newPrj(), "after the delete"))
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 1 {
		t.Errorf("the new cache holds %d memories after one apply, want 1", n)
	}
}

// TestOpenKeepsLiveWAL: a cache file that exists keeps its -wal (it holds
// committed rows not yet checkpointed): a second open reads them.
func TestOpenKeepsLiveWAL(t *testing.T) {
	f := newFixture(t)
	held := f.open("c.db")
	f.apply(held, "repo", "c1", f.memory(newMem(), newPrj(), "kept"))
	other := f.open("c.db")
	if n := count(t, other, `SELECT COUNT(*) FROM "memories"`); n != 1 {
		t.Errorf("a second open of a live cache reads %d memories, want 1", n)
	}
}
