//go:build !windows

package selfupdate

// release must free the upgrade lock at once even while other goroutines
// fork children: a child forked between our open and its exec holds a copy
// of the descriptor, and a close alone left the flock held until it
// exec-ed (the same class as rowfs TestLockReleaseFreesWhileForking).

import (
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLockReleaseFreesWhileForking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.lock")
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = exec.Command("true").Run() // only the fork matters, not the exit status
			}
		}()
	}
	defer func() { stop.Store(true); wg.Wait() }()
	for i := range 300 {
		l, err := tryAcquire(path)
		if err != nil {
			t.Fatalf("round %d: lock not free after the previous release: %v", i, err)
		}
		if err := l.release(); err != nil {
			t.Fatal(err)
		}
	}
}
