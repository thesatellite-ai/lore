package engine

// auditsync.go — one durability flush per audit file for the events of
// one write (owner item C2).
//
//	finishW(evs: 2 or more) ─▶ ctx = withAuditSync(ctx)
//	   per event: sink.Record(ctx, ev) ─▶ the sink appends its line and,
//	              seeing AuditSyncOf(ctx), hands its flush to the group
//	              (one per file, by key) instead of flushing now
//	   group.flush() ─▶ each file flushed once ─▶ then the journal ack
//
// Why: a bulk write (lore migrate copying 1,300 v0.1.17 machine-local rows
// in one staged batch) recorded each row's audit line with its own fsync,
// and on macOS Go's File.Sync is F_FULLFSYNC, a full device flush per row.
// The lines are still written in order and made durable before the
// backend's journal records are acknowledged, so a crash loses nothing the
// journal does not still hold. A sink that ignores the group flushes per
// line as before.

import (
	"context"
	"errors"
	"sync"
)

// AuditSync collects the flushes the sinks deferred while recording the
// events of one write: one per key (an audit file's path), run in the
// order first deferred.
type AuditSync struct {
	mu    sync.Mutex
	keys  []string
	funcs map[string]func() error
}

type auditSyncKey struct{}

// withAuditSync returns ctx carrying a new group and the group.
func withAuditSync(ctx context.Context) (context.Context, *AuditSync) {
	g := &AuditSync{funcs: map[string]func() error{}}
	return context.WithValue(ctx, auditSyncKey{}, g), g
}

// AuditSyncOf returns the group of ctx: a sink recording under it may
// defer its flush (Defer) instead of flushing each line.
func AuditSyncOf(ctx context.Context) (*AuditSync, bool) {
	g, ok := ctx.Value(auditSyncKey{}).(*AuditSync)
	return g, ok && g != nil
}

// Defer registers flush under key; a key already registered keeps its
// first flush (one flush covers every line written to that file).
func (g *AuditSync) Defer(key string, flush func() error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.funcs[key]; ok {
		return
	}
	g.keys = append(g.keys, key)
	g.funcs[key] = flush
}

// flush runs every deferred flush once and forgets them; the errors joined.
func (g *AuditSync) flush() error {
	g.mu.Lock()
	keys, funcs := g.keys, g.funcs
	g.keys, g.funcs = nil, map[string]func() error{}
	g.mu.Unlock()
	var out []error
	for _, k := range keys {
		if err := funcs[k](); err != nil {
			out = append(out, err)
		}
	}
	return errors.Join(out...)
}
