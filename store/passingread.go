package store

import "context"

// A passing read is a full read whose caller uses each row once and lets
// it go: a cache built from nothing reads every store folder in turn and
// hands the rows to the cache. A backend that keeps its reads for later
// readers (the git backend's kept full read) does not keep a passing read,
// and the engine's read snapshot does not keep it either. Why (bug hunt
// H-284, devco:D-1022): lore doctor's first run on a compacted store of
// 1.26M row files kept the folders it had just written to the cache, up to
// both kept-read budgets (~320 MB of row files), for the whole build; the
// later checks read tables, not those whole folders, and the collector
// scanned the kept bytes again and again.
type passingReadKey struct{}

// WithPassingRead returns ctx marking its reads as passing reads.
func WithPassingRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, passingReadKey{}, true)
}

// PassingRead reports whether ctx marks its reads as passing reads.
func PassingRead(ctx context.Context) bool {
	v, _ := ctx.Value(passingReadKey{}).(bool)
	return v
}
