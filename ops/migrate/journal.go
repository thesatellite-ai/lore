package migrate

// The safety half of `lore migrate` (FORMAT_EVOLUTION rule 4 and section 7):
// backup ref, journal and resume, push, status, rollback and unlock.
//
//	run ─► lock ─► Journal.Begin (backup ref + journal, or resume the interrupted one)
//	    ─► settings ─► live ─► rewrites   (--offline: the repo's rows only, RewriteRepoV1)
//	    ─► Journal.Finish (record what changed; push the store commits unless --no-push/--offline)
//	status   ─► Journal.Status   (versions per place, format, journal, lock)
//	rollback ─► lock ─► Journal.Rollback (refuse with E_MIGRATE_ROLLBACK_LOSES, or restore all)
//	unlock   ─► Journal.Unlock   (only a lock whose process is gone)
//
// Journal is an optional upgrade of Rewriter: a Rewriter without it keeps
// the plain run (no backup, no status/rollback/unlock operations).

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names.
const (
	OpStatus   = "migrate.status"
	OpRollback = "migrate.rollback"
	OpUnlock   = "migrate.unlock"
)

// Journal is the backup, journal and undo side of a migration.
type Journal interface {
	// Begin runs after the lock and before any change of a real run: it
	// writes the backup ref and the journal, or resumes the journal of an
	// interrupted run (Begun.Resumed). offline is recorded (FQ5 7.3).
	Begin(ctx context.Context, offline bool) (Begun, error)
	// RewriteRepoV1 is RewriteV1 on the repo store only (--offline: branch
	// files only, the stores need the network).
	RewriteRepoV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error)
	// Finish records what the run changed, pushes the changed store
	// commits unless push is false, and marks the journal done. A run
	// that changed no data folder and reported count 0 rows removes its
	// backup ref and journal and returns no lines.
	Finish(ctx context.Context, push bool, count int) ([]string, error)
	// Status reports the format of every place, the journal and the lock.
	Status(ctx context.Context) (StatusOut, error)
	// Rollback undoes the last migration (FQ5): before push a local undo,
	// after push new commits; E_MIGRATE_ROLLBACK_LOSES when a migrated
	// row was changed afterwards, and then nothing changes.
	Rollback(ctx context.Context) (RollbackOut, error)
	// Unlock removes this machine's migration lock when the process that
	// holds it is gone (E_MIGRATE_LOCKED while it runs).
	Unlock(ctx context.Context) (UnlockOut, error)
}

// Begun is what Journal.Begin did.
type Begun struct {
	Resumed   bool
	BackupRef string
	Lines     []string
}

// PlaceStatus is one place's row versions (FORMAT_EVOLUTION rule 4 status).
type PlaceStatus struct {
	Place string `json:"place"`
	V1    int    `json:"v1"`
	V2    int    `json:"v2"`
	Newer int    `json:"newer"`
	Total int    `json:"total"`
}

// StatusIn is the input of migrate.status (none).
type StatusIn struct{}

// StatusOut is the output of migrate.status.
type StatusOut struct {
	// Writes is the newest row format this lore writes.
	Writes         int64         `json:"writes"`
	Places         []PlaceStatus `json:"places"`
	Features       []string      `json:"features"`
	MinLoreVersion string        `json:"min_lore_version,omitempty"`
	// Pending lists the v1 rows per core table a run would rewrite.
	Pending []string `json:"pending"`
	// Journal describes the last migration ("" when none).
	Journal string   `json:"journal,omitempty"`
	Lock    string   `json:"lock"`
	Notes   []string `json:"notes"`
}

// RollbackIn is the input of migrate.rollback (none).
type RollbackIn struct{}

// RollbackOut is the output of migrate.rollback.
type RollbackOut struct {
	BackupRef string `json:"backup_ref"`
	// Pushed is true when the migration had reached a remote (new commits).
	Pushed bool     `json:"pushed"`
	Lines  []string `json:"lines"`
	// Kept lists rows created after the migration (left as they are).
	Kept []string `json:"kept"`
}

// UnlockIn is the input of migrate.unlock (none).
type UnlockIn struct{}

// UnlockOut is the output of migrate.unlock.
type UnlockOut struct {
	Released bool   `json:"released"`
	Line     string `json:"line"`
}

// journalOps are the operations Journal adds.
func journalOps(rw Rewriter, j Journal) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpStatus, Summary: "Show the row format of every place, the last migration and the lock",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun,
			Errors: []errs.Code{errs.OldLore, errs.Internal},
			Examples: []op.Example{
				{Title: "what a migration would change", CLI: "lore migrate status", Want: "places:"},
			},
		}, func(ctx context.Context, _ StatusIn) (StatusOut, error) {
			return j.Status(ctx)
		}),
		// migrate.rollback (FQ5): lock (M) → journal's backup → refuse when a
		// migrated row changed since → restore every place → release.
		op.Define(op.Spec{
			Name: OpRollback, Summary: "Undo the last migration (local undo before push, new commits after)",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Errors: []errs.Code{errs.MigrateLocked, errs.MigrateRollbackLoses, errs.NotFound, errs.Internal},
			Examples: []op.Example{
				{Title: "undo the last migration", CLI: "lore migrate rollback", Want: "backup_ref:"},
			},
		}, func(ctx context.Context, _ RollbackIn) (RollbackOut, error) {
			unlock, err := rw.LockMigration(ctx)
			if err != nil {
				return RollbackOut{}, err
			}
			defer func() { _ = unlock() }() // a failed release is reported by the next run
			return j.Rollback(ctx)
		}),
		op.Define(op.Spec{
			Name: OpUnlock, Summary: "Release this machine's stale migration lock (its process is gone)",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Errors: []errs.Code{errs.MigrateLocked, errs.Internal},
			Examples: []op.Example{
				{Title: "after a crashed run", CLI: "lore migrate unlock", Want: "released:"},
			},
		}, func(ctx context.Context, _ UnlockIn) (UnlockOut, error) {
			return j.Unlock(ctx)
		}),
	}
}
