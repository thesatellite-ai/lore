package migrate

import (
	"context"
	"sync"
)

// warnKey carries the warning lines of one `lore migrate run` (WithWarnings).
type warnKey struct{}

// warnings is the lines the run's steps add with Warn.
type warnings struct {
	mu    sync.Mutex
	lines []string
}

// WithWarnings is ctx collecting the lines Warn adds under it, and the
// func that takes them (in order, once). run sets it for every run, so a
// step deep in the Rewriter (a row converted on the way, devco D-766: a
// ref holding a name) reports into the run's Steps without a new return
// value on every Rewriter method.
func WithWarnings(ctx context.Context) (context.Context, func() []string) {
	w := &warnings{}
	return context.WithValue(ctx, warnKey{}, w), func() []string {
		w.mu.Lock()
		defer w.mu.Unlock()
		out := w.lines
		w.lines = nil
		return out
	}
}

// Warn adds line to the run's warnings; outside a run (no WithWarnings)
// it reports false and the caller says it another way.
func Warn(ctx context.Context, line string) bool {
	w, ok := ctx.Value(warnKey{}).(*warnings)
	if !ok {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, line)
	return true
}
