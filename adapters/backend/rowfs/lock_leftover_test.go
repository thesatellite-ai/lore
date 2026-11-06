package rowfs

// The v0.1.17 processlock leftover-file cases (later 623; saas/pkg/aicoder/
// processlock TestAcquire_LeftoverFileNeverBlocks, StaleLockReclaim) on the
// store lock (.lore/lock): the OS lock decides, never the file, so a lock
// file left by a crashed or older lore never blocks a writer, whatever pid
// it records (even our own, alive).

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
