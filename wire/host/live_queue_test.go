package host

// live_queue_test.go — D-159: waiters take the live lock in arrival order.
// Before, each polled live.lock every 50 ms and whoever polled first after
// a release won, so with ten agent sessions in one clone one command could
// wait 30 s while the median waited 3 s.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestLiveLockWaitsForAnOlderWaiter: a free lock is not taken while an
// older waiter is queued; once it leaves, the lock is taken and no ticket
// is left behind.
func TestLiveLockWaitsForAnOlderWaiter(t *testing.T) {
	dir := t.TempDir()
	q := liveQueue{dir: filepath.Join(dir, liveQueueDir)}
	older, err := q.join(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if l, err := acquireLiveLock(ctx, dir); err == nil {
		_ = l.release()
		t.Fatal("took the live lock past an older waiter")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting behind an older waiter: %v, want the caller's deadline", err)
	}
	older.leave()
	l, err := acquireLiveLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("after the older waiter left: %v", err)
	}
	defer func() { _ = l.release() }()
	if names, _ := q.tickets(); len(names) != 0 {
		t.Fatalf("tickets left behind: %v", names)
	}
}

// TestLiveLockPassesAnAbandonedTicket: the ticket of a waiter that died
// (its file is there, its OS lock is not) never blocks the queue.
func TestLiveLockPassesAnAbandonedTicket(t *testing.T) {
	dir := t.TempDir()
	q := liveQueue{dir: filepath.Join(dir, liveQueueDir)}
	if err := os.MkdirAll(q.dir, liveStateMode); err != nil {
		t.Fatal(err)
	}
	dead := fmt.Sprintf("%020d-%d-deadbeef", time.Now().Add(-time.Hour).UnixNano(), 999999)
	if err := os.WriteFile(filepath.Join(q.dir, dead), nil, liveLockMode); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := acquireLiveLock(ctx, dir)
	if err != nil {
		t.Fatalf("an abandoned ticket blocked the lock: %v", err)
	}
	defer func() { _ = l.release() }()
	if _, err := os.Stat(filepath.Join(q.dir, dead)); !os.IsNotExist(err) {
		t.Fatalf("the abandoned ticket is still there: %v", err)
	}
}

// TestLiveLockArrivalOrder: five waiters queued one after another behind a
// held lock take it in the order they came.
func TestLiveLockArrivalOrder(t *testing.T) {
	dir := t.TempDir()
	q := liveQueue{dir: filepath.Join(dir, liveQueueDir)}
	holder, err := acquireLiveLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	const n = 5
	var (
		mu    sync.Mutex
		order []int
		wg    sync.WaitGroup
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := acquireLiveLock(ctx, dir)
			if err != nil {
				t.Errorf("waiter %d: %v", i, err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			_ = l.release()
		}()
		for { // the next waiter comes only once this one is queued
			names, _ := q.tickets()
			if len(names) == i+1 {
				break
			}
			if ctx.Err() != nil {
				t.Fatalf("waiter %d never queued", i)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := holder.release(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i, got := range order {
		if got != i {
			t.Fatalf("waiters took the lock in order %v, want arrival order", order)
		}
	}
	if len(order) != n {
		t.Fatalf("only %d of %d waiters took the lock", len(order), n)
	}
}
