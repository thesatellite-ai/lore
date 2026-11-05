package migrate

// Test seam of the migration (FORMAT_EVOLUTION 7.8): named points a test
// build can stop at, so the race tests can crash a run at a phase boundary
// or pause a rollback between its check and its restore.
//
//	run      ─► M ─► T ─► [team-locked] ─► Begin ─► [begun] ─► settings ─► [settings]
//	         ─► live ─► [live] ─► each table ─► [table] ─► [finish] ─► Finish
//	rollback ─► M ─► T ─► [rollback-locked] ─► Journal.Rollback (wire: [rollback-checked])
//
// The hook rides in the command's context (WithHooks), like the TeamLock:
// no package state, and a context without one costs one map lookup per
// point. Only the CLI's test build installs one (saas/cmd/cli,
// migrate_testhooks_loretest.go); a release build never does.

import "context"

// Point is one named place in a migration.
type Point string

// The points, in the order a run reaches them.
const (
	// PointTeamLocked: M and T are held, nothing changed yet.
	PointTeamLocked Point = "team-locked"
	// PointBegun: the backup ref and journal are written.
	PointBegun Point = "begun"
	// PointSettings: the settings and stores step is done.
	PointSettings Point = "settings"
	// PointLive: the live rows are brought in.
	PointLive Point = "live"
	// PointTable: one migrated table is rewritten (reached once per table).
	PointTable Point = "table"
	// PointFinish: every table is rewritten, Finish (push) not yet run.
	PointFinish Point = "finish"
	// PointRollbackLocked: a rollback holds M and T, nothing checked yet.
	PointRollbackLocked Point = "rollback-locked"
	// PointRollbackChecked: a rollback checked every place (nothing lost)
	// and has not restored anything yet (the composition root reaches it).
	PointRollbackChecked Point = "rollback-checked"
)

// Hooks is what a test build runs at each point. An error stops the
// command there with that error (a crash test kills the process instead).
type Hooks interface {
	At(ctx context.Context, p Point) error
}

// hooksKey is the context key of the command's Hooks.
type hooksKey struct{}

// WithHooks returns ctx carrying h for the migrate operations under it.
func WithHooks(ctx context.Context, h Hooks) context.Context {
	return context.WithValue(ctx, hooksKey{}, h)
}

// At runs the context's hook for p; nil when there is none. Exported for
// the composition root's own points (PointRollbackChecked).
func At(ctx context.Context, p Point) error {
	h, ok := ctx.Value(hooksKey{}).(Hooks)
	if !ok || h == nil {
		return nil
	}
	return h.At(ctx, p)
}
