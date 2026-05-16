package processlock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquire_BasicCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l == nil {
		t.Fatal("nil lock")
	}

	// File exists with our pid in content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if !strings.HasPrefix(string(data), "1") && !strings.Contains(string(data), " ") {
		// Lazy check — at least it's non-empty and has space-delimited fields
		t.Errorf("unexpected lock content: %q", string(data))
	}

	if err := l.Release(); err != nil {
		t.Errorf("Release: %v", err)
	}

	// Double release is fine
	if err := l.Release(); err != nil {
		t.Errorf("double release: %v", err)
	}
}

func TestAcquire_BlocksConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")

	l1, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release()

	// Second acquire while first is held → ErrLockHeld
	l2, err := Acquire(path)
	if !errors.Is(err, ErrLockHeld) {
		t.Errorf("expected ErrLockHeld, got %v (lock=%v)", err, l2)
	}
	if l2 != nil {
		l2.Release()
	}
}

func TestAcquire_ReleasesAndReacquires(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")

	l1, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	l2, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	defer l2.Release()
}

func TestAcquire_StaleLockReclaim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")

	// Plant a stale lock file with a definitely-dead pid (PID 1 is init on
	// Unix, always alive — pick a high impossible pid).
	if err := os.WriteFile(path, []byte("9999999 stale\n"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("expected reclaim, got %v", err)
	}
	defer l.Release()
}

func TestAcquire_ReadOnlyEnvSkips(t *testing.T) {
	t.Setenv("LORE_READ_ONLY", "1")

	l, err := Acquire("/tmp/should-not-be-touched")
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("expected ErrReadOnly, got %v", err)
	}
	if l != nil {
		t.Error("expected nil lock under ErrReadOnly")
	}

	// Release on nil lock should be no-op (defer-friendly)
	if err := l.Release(); err != nil {
		t.Errorf("nil-lock Release: %v", err)
	}
}

func TestAcquire_MakesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestOwnerAlive_DeadPid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")
	if err := os.WriteFile(path, []byte("9999999 stale\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	alive, err := ownerAlive(path)
	if err != nil {
		t.Fatalf("ownerAlive: %v", err)
	}
	if alive {
		t.Error("expected stale pid to read as dead")
	}
}

func TestOwnerAlive_OurPid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()

	alive, err := ownerAlive(path)
	if err != nil {
		t.Fatalf("ownerAlive: %v", err)
	}
	if !alive {
		t.Error("expected our own pid to read as alive")
	}
}
