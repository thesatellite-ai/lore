package wire

// commandpush.go — one push per command (store growth item 4, SG-4
// follow-up). A store in push mode `now` pushed after every write
// (storePush.pushAfterWrite runs per Backend.Write), so a command that
// writes several times (task split, member add, an import that flushes a
// task-tree record, a batch undo) pushed several times: one network round
// trip and one remote commit each. App.Run now collects the pushes its
// writes ask for and sends them once, when the operation returns.
//
//	App.Run ─▶ withCommandPush ─▶ op ─▶ Write ─▶ commit ─▶ pushAfterWrite
//	                                     (mode, hold, back-off decided here)
//	                                     ─▶ deferPush: wait for the end
//	        ◀─ op returns (ok or failed) ─▶ flush: one pushNow per store
//
// A command that fails part-way pushes what it committed: its commits are
// in the clone either way, and before this change the writes made before
// the failure had been pushed already, so the remote keeps getting every
// commit a command left behind (no commit waits silently for `lore sync`
// because of an unrelated error later in the command).

import (
	"context"
	"sync"
)

// commandPushKey is the context key of a command's push collector.
type commandPushKey struct{}

// commandPushes is the set of store clones a command's writes asked to
// push, in the order they first asked.
type commandPushes struct {
	mu      sync.Mutex
	closed  bool
	waiting []*storePush
	// ctx is the context of the last deferred write: the operation's own,
	// with the values its sync reads (migration, set-up and leak marks),
	// which App.Run's outer context does not carry.
	ctx map[*storePush]context.Context
}

// withCommandPush returns ctx under which a store's push after a write
// waits for flush, and flush, which pushes every waiting store once. A
// context that already collects (a nested Run) is returned as it is with
// a flush that does nothing: the outer command pushes.
func withCommandPush(ctx context.Context) (context.Context, func(context.Context)) {
	if _, ok := ctx.Value(commandPushKey{}).(*commandPushes); ok {
		return ctx, func(context.Context) {}
	}
	c := &commandPushes{ctx: map[*storePush]context.Context{}}
	return context.WithValue(ctx, commandPushKey{}, c), c.flush
}

// deferPush records that b has a write to push at the end of ctx's
// command and reports whether it did (false: no command collects, push
// now).
func deferPush(ctx context.Context, b *storePush) bool {
	c, ok := ctx.Value(commandPushKey{}).(*commandPushes)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if _, seen := c.ctx[b]; !seen {
		c.waiting = append(c.waiting, b)
	}
	c.ctx[b] = ctx
	b.pendingPush = true
	return true
}

// flush pushes each waiting store once. outer is the command's context: a
// command cancelled (Ctrl-C) pushes nothing and its commits wait for `lore
// sync`; otherwise each push runs under its last write's context, cut off
// when outer is.
func (c *commandPushes) flush(outer context.Context) {
	c.mu.Lock()
	c.closed = true
	waiting := c.waiting
	c.waiting = nil
	c.mu.Unlock()
	for _, b := range waiting {
		if !b.pendingPush {
			continue // an explicit Sync later in the command pushed it
		}
		b.pendingPush = false
		if outer.Err() != nil {
			b.sayLocal()
			continue
		}
		pushBound(c.ctx[b], outer, b)
	}
}

// pushBound runs b's push under ctx's values with outer's cancellation:
// the write's context may be one the operation already cancelled (a
// deferred cancel), while the command itself is still running.
func pushBound(ctx, outer context.Context, b *storePush) {
	c, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(outer, cancel)
	defer stop()
	b.pushNow(c)
}
