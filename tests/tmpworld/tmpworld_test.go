package tmpworld

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A world with a 0500 folder (a read-only ~/.lore shape) is removed whole;
// plain os.RemoveAll left it behind.
func TestRemoveAllReadOnlyFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permission bits do not block deletes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root deletes from read-only folders anyway")
	}
	world := filepath.Join(t.TempDir(), "lm-world")
	locked := filepath.Join(world, "h", ".lore")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "config.yaml"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.RemoveAll(world) == nil {
		t.Fatal("os.RemoveAll removed a world with a 0500 folder: the test proves nothing here")
	}
	if err := RemoveAll(world); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(world); !os.IsNotExist(err) {
		t.Errorf("the world is still there (%v)", err)
	}
	if err := RemoveAll(world); err != nil {
		t.Errorf("RemoveAll of a gone folder: %v", err)
	}
}

// SweepStale removes only the suite's own folders older than the limit.
func TestSweepStale(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // os.TempDir on unix
	if runtime.GOOS == "windows" {
		t.Setenv("TMP", tmp)
	}
	old := time.Now().Add(-48 * time.Hour)
	mk := func(name string, at time.Time) string {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Join(p, "h"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := mk("lm-111", old)
	fresh := mk("lm-222", time.Now())
	other := mk("journeys-bin-333", old)
	if n := SweepStale("lm-", 12*time.Hour); n != 1 {
		t.Errorf("SweepStale removed %d folders, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a 2-day-old world of this suite was kept")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: a fresh world or another suite's folder must stay", filepath.Base(p))
		}
	}
}
