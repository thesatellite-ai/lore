package gitref

import "context"

// WriteObserver is told about every git command Exec runs under a context
// that carries it (WithWriteObserver) and that ReadCache would not treat as
// a read: once before the command starts and once after it ends. It exists
// for ReadCache owners: a cache only sees the writes that go through
// itself, while the same operation may also write the repository through
// another Git value (an Exec of its own, a Repo opened without the cache,
// a delegate package). An owner that puts an observer on the operation's
// context drops its cached answers on each call, so a write through any
// Exec under that context can never leave a stale answer behind, whoever
// made it (wire/host's live run does this for its sessions).
//
// The observer runs on the caller's goroutine, must be cheap and safe for
// concurrent use, and must not run git itself.
type WriteObserver func(Invocation)

// writeObserverKey carries the WriteObserver on a context.
type writeObserverKey struct{}

// WithWriteObserver returns ctx carrying obs; a nil obs returns ctx as it
// is. A later WithWriteObserver on a derived context replaces it there.
func WithWriteObserver(ctx context.Context, obs WriteObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, writeObserverKey{}, obs)
}

// observeWrite returns the observer to tell about inv, or nil when ctx
// carries none or inv is a read ReadCache may keep (classify): the same
// rule as the cache's own, so whatever would drop the cache when run
// through it also drops it when run beside it.
func observeWrite(ctx context.Context, inv Invocation) WriteObserver {
	obs, _ := ctx.Value(writeObserverKey{}).(WriteObserver)
	if obs == nil || classify(inv) != classWrite {
		return nil
	}
	return obs
}
