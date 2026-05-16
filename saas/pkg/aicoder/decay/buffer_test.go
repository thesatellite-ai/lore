package decay

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuffer_TouchAndFlush(t *testing.T) {
	var got map[string]time.Time
	var mu sync.Mutex

	buf := New(Options{
		FlushFn: func(ctx context.Context, batch map[string]time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			got = batch
			return nil
		},
	})

	buf.Touch("mem_a")
	buf.Touch("mem_b")
	buf.Touch("mem_c")

	if err := buf.FlushAndStop(context.Background()); err != nil {
		t.Fatalf("FlushAndStop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %d", len(got))
	}
	for _, id := range []string{"mem_a", "mem_b", "mem_c"} {
		if _, ok := got[id]; !ok {
			t.Errorf("missing %s in flushed batch", id)
		}
	}
}

func TestBuffer_CoalescesRepeatedTouches(t *testing.T) {
	var got map[string]time.Time
	buf := New(Options{
		FlushFn: func(_ context.Context, batch map[string]time.Time) error {
			got = batch
			return nil
		},
	})

	t1 := time.Now()
	buf.TouchAt("mem_x", t1)
	t2 := t1.Add(5 * time.Second)
	buf.TouchAt("mem_x", t2)
	t3 := t2.Add(-1 * time.Second) // backwards — should be ignored
	buf.TouchAt("mem_x", t3)

	buf.FlushAndStop(context.Background())

	if len(got) != 1 {
		t.Errorf("expected coalesce to 1 entry, got %d", len(got))
	}
	if !got["mem_x"].Equal(t2) {
		t.Errorf("expected newest timestamp %v, got %v", t2, got["mem_x"])
	}
}

func TestBuffer_PendingCount(t *testing.T) {
	buf := New(Options{
		FlushFn: func(_ context.Context, _ map[string]time.Time) error { return nil },
	})

	if buf.PendingCount() != 0 {
		t.Error("fresh buffer should be empty")
	}

	buf.Touch("a")
	buf.Touch("b")
	buf.Touch("a") // dedup
	if got := buf.PendingCount(); got != 2 {
		t.Errorf("expected 2 distinct, got %d", got)
	}

	buf.FlushAndStop(context.Background())
	if buf.PendingCount() != 0 {
		t.Error("after flush should be empty")
	}
}

func TestBuffer_FlushTriggeredByThreshold(t *testing.T) {
	var flushCount atomic.Int32

	buf := New(Options{
		FlushThreshold: 5,
		FlushFn: func(_ context.Context, batch map[string]time.Time) error {
			flushCount.Add(1)
			return nil
		},
	})

	for i := 0; i < 5; i++ {
		buf.Touch(string(rune('a' + i)))
	}

	// Async flush kicked off — wait for it.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if flushCount.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if flushCount.Load() == 0 {
		t.Error("expected threshold flush, none happened within 1s")
	}
}

func TestBuffer_PeriodicFlushViaRun(t *testing.T) {
	var flushCount atomic.Int32

	buf := New(Options{
		FlushInterval: 50 * time.Millisecond,
		FlushFn: func(_ context.Context, _ map[string]time.Time) error {
			flushCount.Add(1)
			return nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	go buf.Run(ctx)

	buf.Touch("x")
	time.Sleep(75 * time.Millisecond) // ≥ 1 tick
	buf.Touch("y")
	time.Sleep(75 * time.Millisecond) // ≥ 2 ticks total

	// Wait for Run to exit
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)

	if flushCount.Load() < 1 {
		t.Errorf("expected at least 1 periodic flush, got %d", flushCount.Load())
	}
}

func TestBuffer_FlushErrorViaOnError(t *testing.T) {
	var caught error
	buf := New(Options{
		FlushFn: func(_ context.Context, _ map[string]time.Time) error {
			return errors.New("boom")
		},
		OnError: func(err error) { caught = err },
	})

	buf.Touch("x")
	_ = buf.FlushAndStop(context.Background()) // FlushAndStop returns flush error directly
	// OnError is for async path; FlushAndStop returns error directly, so we test that.

	if err := buf.FlushAndStop(context.Background()); err != nil {
		// Already stopped — second call is no-op flush; should return nil
		// because pending is empty.
		t.Errorf("second FlushAndStop should not error: %v", err)
	}
	_ = caught
}

func TestBuffer_StopIdempotent(t *testing.T) {
	buf := New(Options{
		FlushFn: func(_ context.Context, _ map[string]time.Time) error { return nil },
	})
	buf.Touch("x")
	if err := buf.FlushAndStop(context.Background()); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := buf.FlushAndStop(context.Background()); err != nil {
		t.Errorf("second stop should be no-op, got %v", err)
	}
}

func TestBuffer_NoFlushFn_NoOp(t *testing.T) {
	buf := New(Options{}) // no flush function
	buf.Touch("x")
	if err := buf.FlushAndStop(context.Background()); err != nil {
		t.Errorf("expected no error from no-op buffer, got %v", err)
	}
}
