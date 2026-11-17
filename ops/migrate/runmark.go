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

// commandKey marks a context as the `lore migrate run` command from its
// first step (WithCommand), before run itself starts.
type commandKey struct{}

// WithCommand marks ctx as belonging to `lore migrate run` from the
// command's start, so the store reads the command makes before run (the
// caller's identity, the repo state) are covered too (devco:D-521).
//
// Why: only push and sync move a remote. A store clone's first read in a
// writing command retried the push of commits that already waited there
// (push mode now after an offline write), so a run with nothing to
// migrate sent them. Under this mark a read fetches and merges only; the
// run pushes just the store commits it makes itself (its Finish).
func WithCommand(ctx context.Context) context.Context {
	return context.WithValue(ctx, commandKey{}, true)
}

// InCommand reports WithCommand's mark.
func InCommand(ctx context.Context) bool {
	v, _ := ctx.Value(commandKey{}).(bool) // absent: not a migration command
	return v
}

// machineLocalKey marks a run given --machine-local.
type machineLocalKey struct{}

// WithMachineLocal marks a run whose person asked to copy the repo's
// v0.1.17 machine-local rows (runs, render histories, sessions) into this
// machine's store even when the checkout is not this machine's checkout
// of the repo (a copy, a scratch clone).
func WithMachineLocal(ctx context.Context) context.Context {
	return context.WithValue(ctx, machineLocalKey{}, true)
}

// MachineLocalAsked reports WithMachineLocal's mark.
func MachineLocalAsked(ctx context.Context) bool {
	v, _ := ctx.Value(machineLocalKey{}).(bool) // absent: not asked
	return v
}

// noPushKey marks a run that must not push (--no-push, --offline).
type noPushKey struct{}

// WithNoPush marks ctx as a migration run whose store commits stay in the
// local clones: every write and sync under it fetches at most, never
// pushes, whatever the store's push mode says. Without it each row write
// of the run pushed as a normal write does, so --no-push only held back
// the run's last push.
func WithNoPush(ctx context.Context) context.Context {
	return context.WithValue(ctx, noPushKey{}, true)
}

// NoPushAsked reports WithNoPush's mark.
func NoPushAsked(ctx context.Context) bool {
	v, _ := ctx.Value(noPushKey{}).(bool) // absent: pushes as usual
	return v
}
