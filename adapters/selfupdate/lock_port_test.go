package selfupdate

// The v0.1.17 processlock tests (saas/pkg/aicoder/processlock
// acquire_logic_test.go, flock_test.go), ported before phase E deletes
// them onto the upgrade lock that took over that code (lock.go). The OS
// lock decides, never the file: a leftover file never blocks, a held lock
// is busy whatever pid its file records and the busy answer leaves the file
// alone, LORE_READ_ONLY=1 skips the lock, the parent dir is created, and
// release is idempotent and always closes the file.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// errPortOpenFailed stands in for a non-busy OS error from the lock primitive.
var errPortOpenFailed = errors.New("open failed")

// portDeadPidContent is a lock file written by a process that no longer
// exists (pid far above any real pid limit).
const portDeadPidContent = "9999999 2026-01-01T00:00:00Z\n"

// portFakeTry returns a tryLockFunc that answers with result (nil = success,
// opening the real file) and counts its calls.
func portFakeTry(t *testing.T, result error, calls *int) tryLockFunc {
	t.Helper()
	return func(path string) (*upgradeLock, error) {
		*calls++
		if result != nil {
			return nil, result
		}
		file, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, cliFileMode)
		if err != nil {
			t.Fatalf("fake open: %v", err)
		}
		return &upgradeLock{path: path, file: file, owned: true}, nil
	}
}

// TestUpgradeLockDecisionMatrix: OS lock result (free, busy, other error) x
// whether a lock file already exists. Busy is errLockHeld and never touches
// the file; another OS error passes through; the primitive runs once.
func TestUpgradeLockDecisionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		content string // "" = no file planted
		result  error
		wantErr error // nil = lock acquired
	}{
		{name: "free lock, no file"},
		{name: "free lock, leftover dead-pid file", content: portDeadPidContent},
		{name: "busy, no file", result: errLockBusy, wantErr: errLockHeld},
		{name: "busy, dead-pid file", content: portDeadPidContent, result: errLockBusy, wantErr: errLockHeld},
		{name: "busy, unparseable file", content: "garbage\n", result: errLockBusy, wantErr: errLockHeld},
		{name: "os error passes through", result: errPortOpenFailed, wantErr: errPortOpenFailed},
		{name: "os error passes through, file present", content: portDeadPidContent, result: errPortOpenFailed, wantErr: errPortOpenFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock")
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), cliFileMode); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			l, err := acquire(path, portFakeTry(t, tc.result, &calls))
			if calls != 1 {
				t.Errorf("lock primitive called %d times, want 1", calls)
			}
			if tc.wantErr == nil {
				if err != nil || l == nil {
					t.Fatalf("want lock, got %v (lock=%v)", err, l)
				}
				if rerr := l.release(); rerr != nil {
					t.Fatalf("release: %v", rerr)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if l != nil {
				t.Fatalf("want nil lock on error, got %v", l)
			}
			if rerr := l.release(); rerr != nil { // defer-friendly no-op
				t.Fatalf("release of nil lock: %v", rerr)
			}
			if tc.content != "" {
				got, rerr := os.ReadFile(filepath.Clean(path))
				if rerr != nil || string(got) != tc.content {
					t.Errorf("lock file changed on error: %q, %v", got, rerr)
				}
			}
		})
	}
}

// TestUpgradeLockBusyDeadPidFileNotDeleted: the holder's pid line is
// overwritten with a dead pid (the window between a new holder locking and
// writing its pid); a second acquire still refuses and never deletes or
// replaces the file, or a third would lock a fresh file beside the holder.
func TestUpgradeLockBusyDeadPidFileNotDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	held, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = held.release() })
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(path, []byte(portDeadPidContent), cliFileMode); werr != nil {
		t.Fatal(werr)
	}
	for i := range 2 {
		l, aerr := acquireLock(path)
		if !errors.Is(aerr, errLockHeld) {
			_ = l.release()
			t.Fatalf("attempt %d: want errLockHeld, got %v", i+1, aerr)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file gone: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock file was replaced while held")
	}
}

// TestUpgradeLockReleaseUnlocksOnceAndAlwaysCloses: the explicit unlock runs
// exactly once across repeated releases, and the file is closed even when
// the unlock fails.
func TestUpgradeLockReleaseUnlocksOnceAndAlwaysCloses(t *testing.T) {
	errUnlock := errors.New("unlock failed")
	for _, tc := range []struct {
		name      string
		unlockErr error
	}{
		{name: "unlock ok"},
		{name: "unlock fails", unlockErr: errUnlock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "lock"))
			if err != nil {
				t.Fatal(err)
			}
			unlocks := 0
			l := &upgradeLock{path: file.Name(), file: file, owned: true, unlock: func() error {
				unlocks++
				return tc.unlockErr
			}}
			err = l.release()
			if tc.unlockErr == nil && err != nil {
				t.Fatalf("first release = %v, want nil", err)
			}
			if tc.unlockErr != nil && !errors.Is(err, tc.unlockErr) {
				t.Fatalf("first release = %v, want %v", err, tc.unlockErr)
			}
			if err := l.release(); err != nil {
				t.Fatalf("second release = %v, want nil", err)
			}
			if unlocks != 1 {
				t.Errorf("unlock ran %d times, want 1", unlocks)
			}
			if _, err := file.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
				t.Errorf("file still open after release: write err = %v", err)
			}
		})
	}
}

// TestUpgradeLockLeftoverFileNeverBlocks: a lock file that exists but is not
// OS-locked (an older or crashed lore) never blocks, even when its recorded
// pid is alive (our own), on every pass.
func TestUpgradeLockLeftoverFileNeverBlocks(t *testing.T) {
	for _, content := range []string{
		strconv.Itoa(os.Getpid()) + " 2020-01-01T00:00:00Z\n",
		"9999999 stale\n",
	} {
		path := filepath.Join(t.TempDir(), "lock")
		if err := os.WriteFile(path, []byte(content), cliFileMode); err != nil {
			t.Fatal(err)
		}
		for i := range 3 {
			l, err := acquireLock(path)
			if err != nil {
				t.Fatalf("%q pass %d: %v", content, i+1, err)
			}
			if err := l.release(); err != nil {
				t.Fatalf("%q pass %d release: %v", content, i+1, err)
			}
		}
	}
}

// TestUpgradeLockCycle: acquire writes our pid first, a second acquire while
// held is errLockHeld, release is idempotent, and the lock is taken again at
// once after release.
func TestUpgradeLockCycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l1, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if !strings.HasPrefix(string(data), strconv.Itoa(os.Getpid())+" ") {
		t.Errorf("lock content = %q, want our pid first", data)
	}
	if l2, herr := acquireLock(path); !errors.Is(herr, errLockHeld) || l2 != nil {
		t.Fatalf("second acquire while held = %v (lock=%v), want errLockHeld", herr, l2)
	}
	if rerr := l1.release(); rerr != nil {
		t.Fatalf("release: %v", rerr)
	}
	if rerr := l1.release(); rerr != nil {
		t.Fatalf("double release: %v", rerr)
	}
	l3, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := l3.release(); err != nil {
		t.Fatal(err)
	}
}

// TestUpgradeLockReadOnlySkips: LORE_READ_ONLY=1 returns errReadOnly and
// creates nothing (not even the parent dir); release of the nil lock is a
// no-op. Any other value takes the lock.
func TestUpgradeLockReadOnlySkips(t *testing.T) {
	t.Setenv("LORE_READ_ONLY", "1")
	dir := filepath.Join(t.TempDir(), "never")
	l, err := acquireLock(filepath.Join(dir, "lock"))
	if !errors.Is(err, errReadOnly) || l != nil {
		t.Fatalf("read-only acquire = %v (lock=%v), want errReadOnly", err, l)
	}
	if rerr := l.release(); rerr != nil {
		t.Fatalf("nil-lock release: %v", rerr)
	}
	if _, serr := os.Stat(dir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("read-only acquire touched the disk: %v", serr)
	}
	t.Setenv("LORE_READ_ONLY", "0")
	l, err = acquireLock(filepath.Join(dir, "lock"))
	if err != nil {
		t.Fatalf("LORE_READ_ONLY=0 acquire: %v", err)
	}
	if err := l.release(); err != nil {
		t.Fatal(err)
	}
}

// TestUpgradeLockMakesParentDir: a missing parent chain is created.
func TestUpgradeLockMakesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "nested", "lock")
	l, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.release() })
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}
