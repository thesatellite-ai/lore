package signals

import (
	"context"
	"testing"
	"time"
)

// fixedJitter returns n-1, the top of the range (deterministic schedule).
type fixedJitter struct{}

func (fixedJitter) Int63n(n int64) int64 { return n - 1 }

// zeroJitter always waits nothing (full jitter's lower end).
type zeroJitter struct{}

func (zeroJitter) Int63n(int64) int64 { return 0 }

// stillClock never moves, like a test clock.
type stillClock struct{ t time.Time }

func (c stillClock) Now() time.Time { return c.t }

// TestRetryScheduleIsExponentialFullJitterCapped (devco:D-529): retry n
// waits at most min(Cap, Base·2^(n-1)), the jitter draws from the whole
// range down to zero, and the budget keeps a lost race retried for the
// time a burst of twelve agents needs, not a handful of attempts.
func TestRetryScheduleIsExponentialFullJitterCapped(t *testing.T) {
	r := Retry{Base: 50 * time.Millisecond, Cap: 1500 * time.Millisecond, Budget: 15 * time.Second, MaxAttempts: 60}
	want := []time.Duration{50, 100, 200, 400, 800, 1500, 1500, 1500}
	for i, w := range want {
		if got := r.Delay(i+1, fixedJitter{}); got != w*time.Millisecond {
			t.Fatalf("retry %d waits up to %s, want %s", i+1, got, w*time.Millisecond)
		}
	}
	if got := r.Delay(3, zeroJitter{}); got != 0 {
		t.Fatalf("full jitter reaches zero, got %s", got)
	}
	if got := r.Delay(1000, nil); got != r.Cap {
		t.Fatalf("a huge retry number stays at the cap, got %s", got)
	}

	var slept []time.Duration
	s := &Store{Clock: stillClock{time.Unix(0, 0)}, Retry: r, Jitter: fixedJitter{},
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	b := s.NewBudget()
	n := 0
	for !b.Spent(5) {
		n++
		if err := b.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// 50+100+200+400+800 = 1.55s, then nine 1.5s waits pass 15s: 14 retries.
	if n != 14 || b.Elapsed() < r.Budget {
		t.Fatalf("budget allowed %d retries over %s, want 14 over at least %s (waits %v)", n, b.Elapsed(), r.Budget, slept)
	}
	if DefaultRetry.Budget < 10*time.Second {
		t.Fatalf("the default budget %s is too short for a burst", DefaultRetry.Budget)
	}

	// A jitter of zero and a still clock still end, at MaxAttempts.
	s.Jitter = zeroJitter{}
	b = s.NewBudget()
	n = 0
	for !b.Spent(5) {
		n++
		_ = b.Wait(context.Background())
	}
	if n != r.MaxAttempts-1 {
		t.Fatalf("zero waits stopped after %d retries, want %d", n, r.MaxAttempts-1)
	}
}

// TestBudgetRetriesAtLeastPushRetries: lease.push_retries lost races are
// always retried, even when the time budget is already spent.
func TestBudgetRetriesAtLeastPushRetries(t *testing.T) {
	s := &Store{Clock: stillClock{time.Unix(0, 0)}, Retry: Retry{Budget: time.Nanosecond}, Jitter: fixedJitter{},
		Sleep: func(context.Context, time.Duration) error { return nil }}
	b := s.NewBudget()
	for i := 1; i <= 3; i++ {
		if b.Spent(3) {
			t.Fatalf("stopped after %d lost races, want at least 3 retries", i)
		}
		_ = b.Wait(context.Background())
	}
	if !b.Spent(3) {
		t.Fatal("past push_retries with the budget spent, retrying must stop")
	}
}
