//go:build windows

package selfupdate

// The v0.1.17 processlock Windows tests (saas/pkg/aicoder/processlock
// flock_windows_test.go), ported before phase E onto the upgrade lock's
// LockFileEx path (lock_windows.go).

import (
	"errors"
	"os"
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

// closeReleasePoll is the pause between two tries while waiting.
const closeReleasePoll = 20 * time.Millisecond

// TestWindowsUpgradeLockTwoHandlesContend: a second handle in the same
// process is refused while the first holds the range, the holder's pid
// stays readable, and release frees the lock.
func TestWindowsUpgradeLockTwoHandlesContend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l1, err := tryAcquire(path)
	if err != nil {
		t.Fatalf("first tryAcquire: %v", err)
	}
	if _, berr := tryAcquire(path); !errors.Is(berr, errLockBusy) {
		t.Fatalf("second tryAcquire = %v, want errLockBusy", berr)
	}
	if _, herr := acquireLock(path); !errors.Is(herr, errLockHeld) {
		t.Fatalf("acquire while held = %v, want errLockHeld", herr)
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read while locked: %v", err)
	}
	if !strings.HasPrefix(string(data), strconv.Itoa(os.Getpid())+" ") {
		t.Fatalf("lock content = %q, want our pid first", data)
	}
	if rerr := l1.release(); rerr != nil {
		t.Fatalf("release: %v", rerr)
	}
	l2, err := tryAcquire(path)
	if err != nil {
		t.Fatalf("tryAcquire after release: %v", err)
	}
	if err := l2.release(); err != nil {
		t.Fatalf("release l2: %v", err)
	}
	if err := l2.release(); err != nil {
		t.Fatalf("double release: %v", err)
	}
}

// TestWindowsUpgradeLockCloseWithoutUnlockFrees models a crashed holder: the
// handle is closed without UnlockFileEx and the lock still becomes free.
func TestWindowsUpgradeLockCloseWithoutUnlockFrees(t *testing.T) {
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
			_ = l2.release()
			return
		}
		if !errors.Is(err, errLockBusy) || time.Now().After(deadline) {
			t.Fatalf("lock not freed by close: %v", err)
		}
		time.Sleep(closeReleasePoll)
	}
}
