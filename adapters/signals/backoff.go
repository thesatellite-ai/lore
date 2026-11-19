package signals

import (
	"context"
	"time"
)

// Retry is the lost-race policy of every coordination write (a claim, a
// hold, a release) and of `task next`'s re-pick rounds (devco:D-529).
//
// Why a time budget and not only an attempt count: six or twelve agents on
// one queue on a loaded machine lose many races in a row, each round costing
// a fetch and a push; a small fixed count ran out in about two seconds and
// failed a real burst with E_CONTENDED. Waits use exponential backoff with
// full jitter (uniform in [0, min(Cap, Base·2^n))), so racing agents spread
// out instead of colliding again, and the first attempt never waits.
type Retry struct {
	// Base is the first retry's upper bound; each later retry doubles it.
	Base time.Duration
	// Cap bounds one wait.
	Cap time.Duration
	// Budget is how long lost races are retried, measured from the first
	// attempt: the larger of the clock's elapsed time and the waits slept.
	Budget time.Duration
	// MaxAttempts is a hard stop whatever the budget says, so a clock that
	// does not move (tests) and a jitter of zero still end.
	MaxAttempts int
}

// DefaultRetry sizes the budget for a burst of at least twelve agents
// claiming from one queue on a slow machine.
var DefaultRetry = Retry{Base: 50 * time.Millisecond, Cap: 1500 * time.Millisecond, Budget: 15 * time.Second, MaxAttempts: 60}

// backoffMaxShift bounds the doubling so the shift never overflows; Base<<6
// is already past any sensible Cap, so Cap still decides.
const backoffMaxShift = 6

// orDefault fills zero fields from DefaultRetry.
func (r Retry) orDefault() Retry {
	if r.Base <= 0 {
		r.Base = DefaultRetry.Base
	}
	if r.Cap <= 0 {
		r.Cap = DefaultRetry.Cap
	}
	if r.Budget <= 0 {
		r.Budget = DefaultRetry.Budget
	}
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = DefaultRetry.MaxAttempts
	}
	return r
}

// Delay is the wait before retry n (n = 1 is the first retry, after the
// first lost race): full jitter below min(Cap, Base·2^(n-1)). A nil j
// returns the upper bound (deterministic, for callers without a source).
func (r Retry) Delay(n int, j Jitter) time.Duration {
	r = r.orDefault()
	if n < 1 {
		n = 1
	}
	d := r.Base << min(n-1, backoffMaxShift)
	if d > r.Cap || d <= 0 {
		d = r.Cap
	}
	if j == nil {
		return d
	}
	return time.Duration(j.Int63n(int64(d) + 1))
}

// Budget tracks one retried operation: when it started and how long it
// has slept. Use Store.NewBudget.
type Budget struct {
	retry    Retry
	start    time.Time
	now      func() time.Time
	slept    time.Duration
	attempts int
	jitter   Jitter
	sleep    func(ctx context.Context, d time.Duration) error
}

// Elapsed is the larger of the clock's elapsed time and the waits slept.
func (b *Budget) Elapsed() time.Duration {
	return max(b.now().Sub(b.start), b.slept)
}

// Attempts is how many attempts were counted so far.
func (b *Budget) Attempts() int { return b.attempts }

// Spent counts one more lost attempt and reports whether retrying must stop:
// minRetries lost attempts are always retried (lease.push_retries), past
// them the time budget decides, and MaxAttempts stops it whatever.
func (b *Budget) Spent(minRetries int) bool {
	b.attempts++
	if b.attempts >= b.retry.MaxAttempts {
		return true
	}
	if b.attempts <= minRetries {
		return false
	}
	return b.Elapsed() >= b.retry.Budget
}

// Wait sleeps the jittered delay before the next attempt, honouring ctx.
func (b *Budget) Wait(ctx context.Context) error {
	d := b.retry.Delay(b.attempts, b.jitter)
	b.slept += d
	if b.sleep != nil {
		return b.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NewBudget starts a retry budget with this store's policy, clock, jitter
// and sleep.
func (s *Store) NewBudget() *Budget {
	now := time.Now
	if s.Clock != nil {
		now = s.Clock.Now
	}
	return &Budget{retry: s.Retry.orDefault(), start: now(), now: now, jitter: s.Jitter, sleep: s.Sleep}
}
