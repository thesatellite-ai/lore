// Package decay provides an in-memory access-time buffer for knowledge tables.
//
// The problem (R27 #1): if every retrieval call updated last_accessed_at on
// the matching rows, every read would become a write, blowing up:
//
//   - WAL growth under read-heavy workloads
//   - flock contention (every read serializes against a writer)
//   - audit-log spam (every retrieval generates an audit entry)
//
// The fix: buffer access timestamps in memory; flush periodically as a single
// batch UPDATE. Loss of the buffer (process crash, SIGKILL) loses at most one
// flush window of access-time precision — acceptable for decay scoring, which
// already has half-life on the order of days.
//
// Flush triggers (any of):
//
//   - Time tick (default 60s)
//   - Threshold (default 1000 buffered entries)
//   - Explicit FlushAndStop on graceful shutdown
//
// Catches: R21 #1, R27 #1.
//
// Usage:
//
//	buf := decay.New(decay.Options{
//	    FlushFn: func(ctx context.Context, batch map[string]time.Time) error {
//	        return repo.UpdateLastAccessedBatch(ctx, batch)
//	    },
//	})
//	go buf.Run(ctx) // start background flush loop
//	// in retrieval:
//	buf.Touch(memoryID)
//	// at shutdown:
//	buf.FlushAndStop(ctx)
package decay

import (
	"context"
	"sync"
	"time"
)

// FlushFunc is the callback invoked when buffered access-time updates need to
// land in storage. The map is entity_id -> most-recent-access-time.
type FlushFunc func(ctx context.Context, batch map[string]time.Time) error

// Options configures a Buffer.
type Options struct {
	// FlushFn is called when the buffer flushes. Required.
	FlushFn FlushFunc

	// FlushInterval is the periodic flush tick. Default: 60s.
	FlushInterval time.Duration

	// FlushThreshold triggers an early flush when the buffer hits this size.
	// Default: 1000.
	FlushThreshold int

	// OnError is called when FlushFn returns a non-nil error. The buffer
	// remains running; the caller decides logging policy. Default: silent.
	OnError func(error)
}

// Buffer aggregates access-time updates in memory until flushed.
type Buffer struct {
	opts    Options
	mu      sync.Mutex
	pending map[string]time.Time

	runOnce  sync.Once // marks whether Run has been called
	running  bool      // set inside runOnce.Do
	stopOnce sync.Once
	stop     chan struct{}
	stopped  chan struct{}
}

// New constructs a Buffer with the given options. Run() must be called to
// start the background flush loop (or FlushAndStop manually drives flushes).
func New(opts Options) *Buffer {
	if opts.FlushInterval == 0 {
		opts.FlushInterval = 60 * time.Second
	}
	if opts.FlushThreshold == 0 {
		opts.FlushThreshold = 1000
	}
	return &Buffer{
		opts:    opts,
		pending: make(map[string]time.Time),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Touch records that entityID was accessed now. Multiple Touches between
// flushes coalesce — only the most recent timestamp wins.
//
// Safe for concurrent callers.
func (b *Buffer) Touch(entityID string) {
	b.TouchAt(entityID, time.Now())
}

// TouchAt is like Touch but uses an explicit timestamp. Useful for tests
// that need deterministic timing.
func (b *Buffer) TouchAt(entityID string, t time.Time) {
	b.mu.Lock()
	prev, ok := b.pending[entityID]
	if !ok || t.After(prev) {
		b.pending[entityID] = t
	}
	overThreshold := len(b.pending) >= b.opts.FlushThreshold
	b.mu.Unlock()

	if overThreshold {
		// Async flush — don't block the read path.
		go func() {
			if err := b.flushNow(context.Background()); err != nil && b.opts.OnError != nil {
				b.opts.OnError(err)
			}
		}()
	}
}

// PendingCount returns the number of distinct entity IDs currently buffered.
// Useful for diagnostics and `aicoder doctor`.
func (b *Buffer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// Run drives periodic flushes until ctx is cancelled OR Stop is called.
// Blocks; typically launched as `go buf.Run(ctx)`.
func (b *Buffer) Run(ctx context.Context) {
	b.runOnce.Do(func() { b.running = true })
	defer close(b.stopped)
	tick := time.NewTicker(b.opts.FlushInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			b.flushNow(context.Background()) //nolint:errcheck // best-effort on shutdown
			return
		case <-b.stop:
			b.flushNow(context.Background()) //nolint:errcheck
			return
		case <-tick.C:
			if err := b.flushNow(ctx); err != nil && b.opts.OnError != nil {
				b.opts.OnError(err)
			}
		}
	}
}

// FlushAndStop performs a final synchronous flush and signals Run (if running)
// to exit. Safe to call multiple times; subsequent calls are no-ops.
func (b *Buffer) FlushAndStop(ctx context.Context) error {
	b.stopOnce.Do(func() {
		close(b.stop)
	})
	// Only wait on stopped channel if Run was ever called. Otherwise stopped
	// will never close and we'd hang forever (when ctx has no deadline).
	if b.running {
		select {
		case <-b.stopped:
		case <-ctx.Done():
		}
	}
	return b.flushNow(ctx)
}

// flushNow drains the buffer and invokes FlushFn synchronously. The buffer
// is cleared BEFORE FlushFn runs — if FlushFn fails, those entries are lost
// (they'd be re-touched on next access anyway, so this is acceptable).
func (b *Buffer) flushNow(ctx context.Context) error {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.pending
	b.pending = make(map[string]time.Time)
	b.mu.Unlock()

	if b.opts.FlushFn == nil {
		return nil // no-op buffer (test helper)
	}
	return b.opts.FlushFn(ctx, batch)
}
