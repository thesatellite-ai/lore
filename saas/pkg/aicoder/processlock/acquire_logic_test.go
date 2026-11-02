package processlock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// errOpenFailed stands in for a non-busy OS error from the lock primitive.
var errOpenFailed = errors.New("open failed")

// deadPidContent is a lock file written by a process that no longer exists
// (pid far above any real pid limit).
const deadPidContent = "9999999 2026-01-01T00:00:00Z\n"

// fakeTry returns a tryLockFunc that answers with result (nil = success,
// opening the real file) and counts its calls.
func fakeTry(t *testing.T, result error, calls *int) tryLockFunc {
	return func(path string) (*Lock, error) {
		*calls++
		if result != nil {
			return nil, result
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			t.Fatalf("fake open: %v", err)
		}
		return &Lock{path: path, file: file, owned: true}, nil
	}
}

// TestAcquireDecisionMatrix covers every branch of acquire: OS lock result
// (free, busy, other error) × whether a lock file already exists. A busy
// lock is reported as held and its file is never touched, whatever pid the
// file records.
func TestAcquireDecisionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		content string // "" = no file planted
		result  error
		wantErr error // nil = lock acquired
	}{
		{name: "free lock, no file"},
		{name: "free lock, leftover dead-pid file", content: deadPidContent},
		{name: "busy, no file", result: errLockBusy, wantErr: ErrLockHeld},
		{name: "busy, dead-pid file", content: deadPidContent, result: errLockBusy, wantErr: ErrLockHeld},
		{name: "busy, unparseable file", content: "garbage\n", result: errLockBusy, wantErr: ErrLockHeld},
		{name: "os error passes through", result: errOpenFailed, wantErr: errOpenFailed},
		{name: "os error passes through, file present", content: deadPidContent, result: errOpenFailed, wantErr: errOpenFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock")
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			l, err := acquire(path, fakeTry(t, tc.result, &calls))
			if calls != 1 {
				t.Errorf("lock primitive called %d times, want 1", calls)
			}
			if tc.wantErr == nil {
				if err != nil || l == nil {
					t.Fatalf("want lock, got %v (lock=%v)", err, l)
				}
				if rerr := l.Release(); rerr != nil {
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
			// Release after a failed Acquire is a defer-friendly no-op.
			if rerr := l.Release(); rerr != nil {
				t.Fatalf("release of nil lock: %v", rerr)
			}
			if tc.content != "" {
				got, rerr := os.ReadFile(path)
				if rerr != nil || string(got) != tc.content {
					t.Errorf("lock file changed on error: %q, %v", got, rerr)
				}
			}
		})
	}
}

// TestAcquire_BusyDeadPidFileNotDeleted pins the two-holders race fix with
// the real OS lock: the holder's pid line is overwritten with a dead pid (as
// happens in the window between a new holder locking and writing its pid),
// and a second Acquire must still refuse without deleting or replacing the
// file — otherwise a third Acquire would lock a fresh file alongside the
// live holder.
func TestAcquire_BusyDeadPidFileNotDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	held, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = held.Release() }()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(deadPidContent), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		l, err := Acquire(path)
		if !errors.Is(err, ErrLockHeld) {
			_ = l.Release()
			t.Fatalf("attempt %d: want ErrLockHeld, got %v", i+1, err)
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

// TestReleaseUnlocksOnceAndAlwaysCloses pins Release's contract: the explicit
// unlock runs exactly once across repeated calls, and the file is closed even
// when unlock fails.
func TestReleaseUnlocksOnceAndAlwaysCloses(t *testing.T) {
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
			l := &Lock{path: file.Name(), file: file, owned: true, unlock: func() error {
				unlocks++
				return tc.unlockErr
			}}
			if err := l.Release(); !errors.Is(err, tc.unlockErr) || (tc.unlockErr == nil && err != nil) {
				t.Fatalf("first release = %v, want %v", err, tc.unlockErr)
			}
			if err := l.Release(); err != nil {
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

// TestAcquire_LeftoverFileNeverBlocks pins the Windows bug from issue #3
// on every OS: a lock file that exists but is not OS-locked (left by an older
// lore, or a crashed one) must never block — even when its recorded pid is
// alive, here our own.
func TestAcquire_LeftoverFileNeverBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	content := strconv.Itoa(os.Getpid()) + " 2020-01-01T00:00:00Z\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // every pass, not just the first
		l, err := Acquire(path)
		if err != nil {
			t.Fatalf("pass %d: %v", i+1, err)
		}
		if err := l.Release(); err != nil {
			t.Fatalf("pass %d release: %v", i+1, err)
		}
	}
}
