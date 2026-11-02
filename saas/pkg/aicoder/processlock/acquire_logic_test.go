package processlock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// errOpenFailed stands in for a non-busy OS error from tryAcquire.
var errOpenFailed = errors.New("open failed")

// fakeOS scripts tryAcquire results and records calls, so the
// acquire/reclaim decision can be exercised on any OS.
type fakeOS struct {
	results []error // per tryAcquire call; nil means success
	calls   int
	alive   bool
}

func (f *fakeOS) primitives(t *testing.T, reclaim bool) primitives {
	return primitives{
		tryAcquire: func(path string) (*Lock, error) {
			i := f.calls
			f.calls++
			if i >= len(f.results) {
				t.Fatalf("unexpected tryAcquire call #%d", i+1)
			}
			if err := f.results[i]; err != nil {
				return nil, err
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				t.Fatalf("fake open: %v", err)
			}
			return &Lock{path: path, file: file, owned: true}, nil
		},
		pidAlive:           func(int) bool { return f.alive },
		reclaimOnDeadOwner: reclaim,
	}
}

// TestAcquireDecisionMatrix covers every branch of acquire across the
// reclaim policy (Unix: on, Windows: off) × owner liveness × file content.
func TestAcquireDecisionMatrix(t *testing.T) {
	const pidContent = "4242 2026-01-01T00:00:00Z\n"
	tests := []struct {
		name        string
		content     string // "" = no file planted
		reclaim     bool
		alive       bool
		results     []error
		wantErr     error // nil = lock acquired
		wantCalls   int
		wantRemoved bool
	}{
		{name: "free lock", results: []error{nil}, wantCalls: 1},
		{name: "free lock, unlocked leftover file", content: pidContent, results: []error{nil}, wantCalls: 1},
		{name: "busy, live owner, reclaim on", content: pidContent, reclaim: true, alive: true, results: []error{errLockBusy}, wantErr: ErrLockHeld, wantCalls: 1},
		{name: "busy, dead owner, reclaim on", content: pidContent, reclaim: true, results: []error{errLockBusy, nil}, wantCalls: 2, wantRemoved: true},
		{name: "busy, dead owner, reclaim on, still busy", content: pidContent, reclaim: true, results: []error{errLockBusy, errLockBusy}, wantErr: ErrLockHeld, wantCalls: 2, wantRemoved: true},
		{name: "busy, unparseable owner, reclaim on", content: "garbage\n", reclaim: true, results: []error{errLockBusy}, wantErr: ErrLockHeld, wantCalls: 1},
		{name: "busy, dead owner, reclaim off", content: pidContent, results: []error{errLockBusy}, wantErr: ErrLockHeld, wantCalls: 1},
		{name: "busy, live owner, reclaim off", content: pidContent, alive: true, results: []error{errLockBusy}, wantErr: ErrLockHeld, wantCalls: 1},
		{name: "os error passes through", results: []error{errOpenFailed}, wantErr: errOpenFailed, wantCalls: 1},
		{name: "os error passes through, reclaim on", content: pidContent, reclaim: true, results: []error{errOpenFailed}, wantErr: errOpenFailed, wantCalls: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock")
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			f := &fakeOS{results: tc.results, alive: tc.alive}
			l, err := acquire(path, f.primitives(t, tc.reclaim))
			if tc.wantErr == nil {
				if err != nil || l == nil {
					t.Fatalf("want lock, got %v (lock=%v)", err, l)
				}
				if rerr := l.Release(); rerr != nil {
					t.Fatalf("release: %v", rerr)
				}
			} else {
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
			}
			if f.calls != tc.wantCalls {
				t.Errorf("tryAcquire calls = %d, want %d", f.calls, tc.wantCalls)
			}
			if tc.content != "" && tc.wantErr != nil {
				_, statErr := os.Stat(path)
				removed := errors.Is(statErr, os.ErrNotExist)
				if removed != tc.wantRemoved {
					t.Errorf("file removed = %v, want %v", removed, tc.wantRemoved)
				}
			}
		})
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
