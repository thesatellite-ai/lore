//go:build windows

package processlock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// closeReleaseWait bounds how long a test waits for the kernel to drop a
// LockFileEx range after its handle is closed without an explicit unlock;
// Microsoft documents that release as possibly delayed.
const closeReleaseWait = 2 * time.Second

// TestWindows_TwoHandlesContend proves the real LockFileEx path: a second
// handle in the same process is refused while the first holds the range,
// the holder's pid stays readable, and Release frees the lock.
func TestWindows_TwoHandlesContend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l1, err := tryAcquire(path)
	if err != nil {
		t.Fatalf("first tryAcquire: %v", err)
	}
	if _, err := tryAcquire(path); !errors.Is(err, errLockBusy) {
		t.Fatalf("second tryAcquire = %v, want errLockBusy", err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Acquire while held = %v, want ErrLockHeld", err)
	}
	data, err := os.ReadFile(path) // content must stay readable while locked
	if err != nil {
		t.Fatalf("read while locked: %v", err)
	}
	if !strings.HasPrefix(string(data), strconv.Itoa(os.Getpid())+" ") {
		t.Fatalf("lock content = %q, want our pid first", data)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	l2, err := tryAcquire(path)
	if err != nil {
		t.Fatalf("tryAcquire after release: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("release l2: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("double release: %v", err)
	}
}

// TestWindows_CloseWithoutUnlockFrees models a crashed holder: the handle is
// closed without UnlockFileEx and the lock must still become free.
func TestWindows_CloseWithoutUnlockFrees(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l1, err := tryAcquire(path)
	if err != nil {
		t.Fatalf("tryAcquire: %v", err)
	}
	if err := l1.file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	deadline := time.Now().Add(closeReleaseWait)
	for {
		l2, err := tryAcquire(path)
		if err == nil {
			_ = l2.Release()
			return
		}
		if !errors.Is(err, errLockBusy) || time.Now().After(deadline) {
			t.Fatalf("lock not freed by close: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWindows_PidAlive checks the OpenProcess/GetExitCodeProcess probe
// against our own pid, an exited child, and invalid pids.
func TestWindows_PidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("own pid reads as dead")
	}
	cmd := exec.Command("cmd", "/c", "exit", "0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run child: %v", err)
	}
	if pidAlive(cmd.Process.Pid) {
		t.Error("exited child reads as alive")
	}
	for _, pid := range []int{0, -1} {
		if pidAlive(pid) {
			t.Errorf("pid %d reads as alive", pid)
		}
	}
}
