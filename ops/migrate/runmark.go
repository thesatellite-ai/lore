package migrate

import "context"

// runKey marks a context as inside `lore migrate run` (WithRun); the type
// is unexported, so only this package can set the mark.
type runKey struct{}

// WithRun marks ctx as carrying `lore migrate run`'s own writes
// (F-MIGRATE-1). run sets it for a run that writes (not --dry-run), right
// after the migration lock is taken, so every step below it (settings,
// machine-local copy, live rows, rewrites, and the session row the first
// audited write creates) runs under it.
//
// Why a mark and not an order of steps: the composition root's O6 format
// guard (FORMAT_EVOLUTION rule 2: "rows written by teammates with
// v0.1.17; the first 0.1.18 write asks") names `lore migrate run` as its
// answer. A run that refused its own writes with that question could only
// finish by being rerun. The mark lets exactly this command's writes pass
// that one guard; every other write still asks.
func WithRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, runKey{}, true)
}

// InRun reports whether ctx carries WithRun's mark: the write belongs to
// `lore migrate run` itself.
func InRun(ctx context.Context) bool {
	v, _ := ctx.Value(runKey{}).(bool) // absent: not a migration run
	return v
}
