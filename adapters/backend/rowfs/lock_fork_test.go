//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package rowfs

// Release must free the store lock at once even while other goroutines of
// the process fork children. flock(2) belongs to the open file
// description: a child forked between our open and its exec holds a copy
// of the descriptor, so a Release that only closes our copy left the lock
// held until that child exec-ed, and the next writer or `lore doctor`
// probe saw it busy (wire TestDoctorStoreLocks failed under -race beside
// parallel tests running git).

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLockReleaseFreesWhileForking(t *testing.T) {
	t.Parallel()
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 8 { // forkers: each exec is a window in which a child holds our fds
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
		l, err := r.Lock(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Release(); err != nil {
			t.Fatal(err)
		}
		st, err := r.LockHolder()
		if err != nil {
			t.Fatal(err)
		}
		if st.Held {
			t.Fatalf("round %d: lock still held right after Release: %+v", i, st)
		}
	}
}
