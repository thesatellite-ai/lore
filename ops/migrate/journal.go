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
//	unlock --team ─► unlockTeam  (teamlock.go: a stale team lock on the signals ref)
//
// Journal is an optional upgrade of Rewriter: a Rewriter without it keeps
// the plain run (no backup, no status/rollback/unlock operations).

import (
	"context"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/thesatellite-ai/lore/logic/plural"
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
	Finish(ctx context.Context, push bool, count int) (Finished, error)
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

// Finished is what Journal.Finish recorded.
type Finished struct {
	// Lines are the journal's lines (one per changed place, undo).
	Lines []string
	// Uncommitted counts the files of the code repo's .lore/data the run
	// changed: lore never commits a code repo, so they wait for the
	// person's commit (0 when the repo store is a store clone lore commits).
	Uncommitted int
}

// CommitLine is a run's last line when it left files in the code repo's
// .lore/data for the person to commit (owner item C2: lore fix ended with
// that hint buried among 5,000 lines, then DEGRADED).
func CommitLine(n int) string {
	verb := " wait"
	if n == 1 {
		verb = " waits"
	}
	return plural.Count(n, "upgraded file") + " in .lore/data" + verb + " for your commit: " + CommitCommand
}

// CommitCommand is the command CommitLine names.
const CommitCommand = `git add .lore/data && git commit -m "lore: migrate rows to format 2"`

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
	Writes int64 `json:"writes"`
	// Format names the project's format (D12): what its committed
	// .lore/data/_meta.json records, or the rows'; who migrated it and
	// when when recorded. ProjectFormat is the number (0: no project).
	Format         string        `json:"format,omitempty"`
	ProjectFormat  int           `json:"project_format,omitempty"`
	Places         []PlaceStatus `json:"places"`
	Features       []string      `json:"features"`
	MinLoreVersion string        `json:"min_lore_version,omitempty"`
	// Pending lists the v1 rows per core table a run would rewrite.
	Pending []string `json:"pending"`
	// Journal describes the last migration ("" when none).
	Journal string `json:"journal,omitempty"`
	// Unfinished is true when this checkout's journal holds a migration
	// that did not run to its end (interrupted, or rolled back): its v1
	// row files are the migration's, not an older lore's writing after
	// it (wire doctor row_format). The journal is machine-local, so a
	// teammate's checkout of a migrated repo has none (false).
	Unfinished bool     `json:"unfinished,omitempty"`
	Lock       string   `json:"lock"`
	Notes      []string `json:"notes"`
}

var _ op.Texter = StatusOut{}

// statusPad is the space between the places table's columns (the CLI's
// generic table padding, so the table looks as it did before Text).
const statusPad = 2

// Text implements op.Texter: the generic key: value form, one line per
// field in the JSON order, but a list with nothing in it (pending,
// notes, features in a shared-database repo) prints no line at all
// (#22c), so no "pending: " line reads as if something were pending.
// --json keeps every key.
func (o StatusOut) Text() string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	line("format", o.Format)
	line("writes", strconv.FormatInt(o.Writes, 10))
	if len(o.Places) > 0 {
		b.WriteString("places:\n")
		tw := tabwriter.NewWriter(&b, 0, 0, statusPad, ' ', 0)
		_, _ = tw.Write([]byte("  PLACE\tV1\tV2\tNEWER\tTOTAL\n")) // a strings.Builder never fails
		for _, p := range o.Places {
			_, _ = tw.Write([]byte("  " + strings.Join([]string{p.Place, strconv.Itoa(p.V1), strconv.Itoa(p.V2), strconv.Itoa(p.Newer), strconv.Itoa(p.Total)}, "\t") + "\n"))
		}
		_ = tw.Flush()
	}
	line("features", strings.Join(o.Features, ", "))
	line("min_lore_version", o.MinLoreVersion)
	line("pending", strings.Join(o.Pending, ", "))
	line("journal", o.Journal)
	line("lock", o.Lock)
	line("notes", strings.Join(o.Notes, ", "))
	return b.String()
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

var _ op.Texter = RollbackOut{}

// Text implements op.Texter: what was undone first, then one indented line
// per place (and the earlier migration still standing, when there is one),
// then the rows left as they are. The generic form printed "lines: …" and
// "kept: …" with every line joined on one.
func (o RollbackOut) Text() string {
	var b strings.Builder
	b.WriteString("Rolled back the migration (backup " + o.BackupRef + ")\n")
	for _, l := range o.Lines {
		b.WriteString("  " + l + "\n")
	}
	if len(o.Kept) > 0 {
		b.WriteString("kept as they are (created after the migration):\n")
		for _, k := range o.Kept {
			b.WriteString("  " + k + "\n")
		}
	}
	return b.String()
}

// UnlockIn is the input of migrate.unlock.
type UnlockIn struct {
	// Team clears a stale team lock on the signals ref instead (7.2); asks first.
	Team bool `json:"team,omitempty" flag:"team" desc:"clear a stale team migration lock on the signals ref (asks first)"`
}

// UnlockOut is the output of migrate.unlock.
type UnlockOut struct {
	Released bool   `json:"released"`
	Line     string `json:"line"`
}

var _ op.Texter = UnlockOut{}

// Text implements op.Texter: the sentence itself. Without it the generic
// renderer printed the field name ("line: no migration lock on this
// machine").
func (o UnlockOut) Text() string {
	if o.Line == "" {
		return ""
	}
	return o.Line + "\n"
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
			Errors: []errs.Code{errs.MigrateLocked, errs.MigrateRollbackLoses, errs.NotFound, errs.NotAStore, errs.Unsupported, errs.Internal},
			Examples: []op.Example{
				{Title: "undo the last migration", CLI: "lore migrate rollback", Want: "backup_ref:"},
			},
		}, func(ctx context.Context, _ RollbackIn) (RollbackOut, error) {
			unlock, err := rw.LockMigration(ctx)
			if err != nil {
				return RollbackOut{}, err
			}
			defer func() { _ = unlock() }()      // a failed release is reported by the next run
			hold, err := takeTeamLock(ctx, true) // 7.2: a rollback cannot race a run
			if err != nil {
				return RollbackOut{}, err
			}
			defer func() { _ = hold.release() }() // an unreleased hold expires (7.2)
			if err := At(ctx, PointRollbackLocked); err != nil {
				return RollbackOut{}, err
			}
			return j.Rollback(ctx)
		}),
		op.Define(op.Spec{
			Name: OpUnlock, Summary: "Release this machine's stale migration lock (its process is gone), or with --team a stale team lock",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Confirm: op.ConfirmRule{When: "team"},
			Errors:  []errs.Code{errs.MigrateLocked, errs.NeedsConfirm, errs.Offline, errs.InvalidInput, errs.NotAStore, errs.Internal},
			Examples: []op.Example{
				{Title: "after a crashed run", CLI: "lore migrate unlock", Want: "released:"},
			},
		}, func(ctx context.Context, in UnlockIn) (UnlockOut, error) {
			// unlock (7.2): machine lock only; --team: unlockTeam (stale M
			// cleared, M taken, then the host clears an expired or own hold).
			if in.Team {
				return unlockTeam(ctx, rw, j)
			}
			return j.Unlock(ctx)
		}),
	}
}
