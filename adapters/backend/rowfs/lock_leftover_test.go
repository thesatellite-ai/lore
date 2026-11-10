package rowfs

// The v0.1.17 processlock leftover-file cases (later 623; saas/pkg/aicoder/
// processlock TestAcquire_LeftoverFileNeverBlocks, StaleLockReclaim) on the
// store lock (.lore/lock): the OS lock decides, never the file, so a lock
// file left by a crashed or older lore never blocks a writer, whatever pid
// it records (even our own, alive). A free lock is taken at once, so the
// 50ms timeout here never decides the outcome, however loaded the machine
// (TestLockFreeNeverTimesOut).

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLockLeftoverFileNeverBlocks(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"dead pid":     "9999999 2026-01-01T00:00:00Z\n",
		"our live pid": strconv.Itoa(os.Getpid()) + "\n",
		"garbage":      "garbage\n",
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			p := filepath.Join(dir, LoreDir, LockFileName)
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := Open(dir, WithLockTimeout(50*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			for pass := range 3 { // every pass, not only the first
				l, lerr := r.Lock(context.Background())
				if lerr != nil {
					t.Fatalf("pass %d: a leftover lock file blocked the writer: %v", pass, lerr)
				}
				if rerr := l.Release(); rerr != nil {
					t.Fatal(rerr)
				}
			}
		})
	}
}

// TestLockFreeNeverTimesOut: a lock nobody holds is taken however short
// the timeout and however late the goroutine runs. A 1ns timeout has
// expired before Lock looks at it, the state a 50ms timeout reaches on a
// loaded machine; it used to be refused about half the time as "the store
// lock stayed held" (a select over a free gate and an expired timer).
func TestLockFreeNeverTimesOut(t *testing.T) {
	t.Parallel()
	r, err := Open(t.TempDir(), WithLockTimeout(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	for pass := range 200 {
		l, lerr := r.Lock(context.Background())
		if lerr != nil {
			t.Fatalf("pass %d: a free lock was refused: %v", pass, lerr)
		}
		if rerr := l.Release(); rerr != nil {
			t.Fatal(rerr)
		}
	}
}
