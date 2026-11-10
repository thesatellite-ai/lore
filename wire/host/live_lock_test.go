//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package host

// The v0.1.17 processlock decision matrix (later 623; saas/pkg/aicoder/
// processlock acquire_logic_test.go, flock_test.go) on the live lock that
// replaced it (.git/lore/live.lock, flock(2)): the OS lock decides, never
// the file. A leftover file (a dead pid, our own live pid, garbage) never
// blocks; a held lock is busy whatever the file says, and the busy answer
// leaves the file as it is.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// deadPidContent is a lock file left by a process that no longer exists.
const deadPidContent = "9999999 2026-01-01T00:00:00Z\n"

func TestLiveLockDecisionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" = no file planted
		held    bool   // another open file description holds the flock
		busy    bool
	}{
		{name: "free, no file"},
		{name: "free, leftover dead-pid file", content: deadPidContent},
		{name: "free, leftover file naming our live pid", content: strconv.Itoa(os.Getpid()) + "\n"},
		{name: "free, garbage file", content: "garbage\n"},
		{name: "held, no file content", held: true, busy: true},
		{name: "held, dead-pid file", content: deadPidContent, held: true, busy: true},
		{name: "held, garbage file", content: "garbage\n", held: true, busy: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), liveLockFile)
			if c.content != "" {
				if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.held {
				holder, err := tryLiveLock(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = holder.release() })
			}
			for pass := range 3 { // every pass, not only the first
				l, err := tryLiveLock(path)
				if c.busy {
					if !errors.Is(err, errLiveLockBusy) || l != nil {
						t.Fatalf("pass %d: %v, want busy", pass, err)
					}
					if c.content != "" {
						if got, rerr := os.ReadFile(filepath.Clean(path)); rerr != nil || string(got) != c.content {
							t.Fatalf("a busy answer changed the lock file: %q %v", got, rerr)
						}
					}
					continue
				}
				if err != nil {
					t.Fatalf("pass %d: %v, want the lock", pass, err)
				}
				if rerr := l.release(); rerr != nil {
					t.Fatal(rerr)
				}
			}
		})
	}
}

// TestAcquireLiveLock: the state dir is created; a released lock is taken
// again at once; a wait on a held lock ends with the caller's context; an
// OS error that is not "busy" is E_INTERNAL, never a wait.
func TestAcquireLiveLock(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "missing", "lore")
	l, err := acquireLiveLock(ctx, dir)
	if err != nil {
		t.Fatalf("missing state dir: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, werr := acquireLiveLock(cancelled, dir); !errors.Is(werr, context.Canceled) {
		t.Fatalf("held lock, cancelled wait: %v", werr)
	}
	if rerr := l.release(); rerr != nil {
		t.Fatal(rerr)
	}
	again, err := acquireLiveLock(ctx, dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if rerr := again.release(); rerr != nil {
		t.Fatal(rerr)
	}
	// The lock path is a directory: open fails with a real error.
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, liveLockFile), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, oerr := acquireLiveLock(ctx, bad); !errs.Has(oerr, errs.Internal) {
		t.Fatalf("lock path is a directory: %v, want E_INTERNAL", oerr)
	}
}
