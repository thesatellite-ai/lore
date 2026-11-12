// Package migrate defines `lore migrate run` (FORMAT_EVOLUTION, layer L5):
// it rewrites row files still in the v0.1.17 format (`_v: 1`) as `_v: 2`.
//
// Reading never needs it: a `_v: 1` file is upcast in memory on every read
// (model/row). Rewriting is a separate, explicit step because a file
// rewritten as `_v: 2` can no longer be read by a teammate still on
// v0.1.17; running this command is the team's "we have all upgraded".
//
// Scope: every table of a migrated noun (Rewriter.MigratedTables) is
// rewritten in the repo store and every attached store; other tables keep `_v: 1`
// until their noun migrates. It also converts the v0.1.17 settings the core
// owns (STORE_SERVER_SPEC 13): ~/.lore/identity.yaml into `identity:` of
// ~/.lore/config.yaml (CFG9) and v1 lore-store.yaml markers into v2 (a
// permanent sto_ id). The run holds the migration lock (E_MIGRATE_LOCKED).
//
// `lore migrate enable <feature>` (FORMAT_EVOLUTION rule 2) declares a
// format feature in .lore/data/_meta.json before its first use:
//
//	enable ─► Rewriter.EnableFeature ─► (O6 open and no --yes: E_NEEDS_CONFIRM)
//	       ─► rowfs.DeclareRepo ─► features ∪ {name}, min_lore_version = max(floor, Since)
//
// run with a Journal (journal.go): lock ─► Begin (backup ref, journal or
// resume) ─► steps ─► Finish (push unless --no-push/--offline); plus
// status, rollback (FQ5) and unlock.
package migrate

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// OpRun is the operation name: `lore migrate run`, POST /v1/migrate/run.
const OpRun = "migrate.run"

// restNoun keeps the REST collection singular (/v1/migrate/run): the noun
// is a verb-like command, its default plural reads wrong.
const restNoun = "migrate"

// Rewriter rewrites the v1 rows of one table in the repo store. The
// composition root implements it with the store backend: a rewrite changes
// only the format (_v, _hlc), so it must NOT go through the engine's Update,
// which would stamp a new HLC and updated_at and so change the row.
type Rewriter interface {
	// MigratedTables lists the tables the core owns (the migrated nouns),
	// in the order migrate.run rewrites them. The composition root keeps
	// the one list, so a new noun needs no line here.
	MigratedTables() []row.Table
	// RewriteV1 returns the ids of the rows of table still in `_v: 1`, and
	// rewrites them as `_v: 2` unless dryRun. A row changed concurrently
	// is not overwritten (the backend's Expect check): the call fails with
	// E_STALE and can be rerun.
	RewriteV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error)
	// MigrateSettings converts the v0.1.17 settings and stores that are
	// not plain row rewrites (Mode B, the personal store flatten,
	// identity.yaml, v1 store markers, the org roster and defaults, the
	// entity_tags links) and returns one line per change plus how many
	// rows those changes write or move (made, or that would be made under
	// dryRun).
	MigrateSettings(ctx context.Context, dryRun bool) (Settings, error)
	// MigrateLive brings the v0.1.17 live ref's rows of core-owned tables
	// (open tasks and the like, never landed in .lore/data) into the repo
	// store as `_v: 2` rows with the same id and stamp, and returns
	// "table/id" per row (made, or that would be made under dryRun).
	MigrateLive(ctx context.Context, dryRun bool) ([]string, error)
	// EnableFeature declares a known format feature in the repo's
	// _meta.json before its first use (`lore migrate enable`); it answers
	// E_NEEDS_CONFIRM when teammates would become read-only and the caller
	// has not confirmed.
	EnableFeature(ctx context.Context, name string) (EnableOut, error)
	// LockMigration takes the migration lock (E_MIGRATE_LOCKED when held);
	// the returned func releases it.
	LockMigration(ctx context.Context) (func() error, error)
	// WriteGate refuses (E_OLD_LORE) when this binary may not write any
	// attached store: the same format gate as every engine write. A run
	// rewrites files outside the engine's Update, so it asks here first
	// (INTEG-MIGREST-1): an older lore never changes the format of a
	// project a newer lore owns.
	WriteGate(ctx context.Context) error
}

// Settings is what Rewriter.MigrateSettings did (or would do).
type Settings struct {
	// Steps are the human lines, one per change.
	Steps []string
	// Rows counts every row the steps wrote or moved (a Mode B row copied
	// into its store, a personal row moved to the root, an org roster row
	// rewritten, a routing policy row written, a row whose tag_ids were
	// set). A file that is not a row (a marker, config.yaml) counts 0.
	Rows int
}

// RunIn is the input of migrate.run.
type RunIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list the files that would be rewritten; change nothing"`
	// NoPush keeps the store commits local (rollback is then a local undo).
	NoPush bool `json:"no_push,omitempty" flag:"no-push" desc:"commit the store changes but do not push them"`
	// Offline rewrites the repo's branch files only (FORMAT_EVOLUTION 7.3).
	Offline bool `json:"offline,omitempty" flag:"offline" desc:"branch files only: skip the settings, live rows and stores (they need the network)"`
	// MachineLocal copies the repo's v0.1.17 machine-local rows into this
	// machine's store even from a checkout that is not this machine's own
	// (wire machinecopy.go says when it is).
	MachineLocal bool `json:"machine_local,omitempty" flag:"machine-local" desc:"also copy the repo's v0.1.17 machine-local rows (runs, render histories, sessions) into this machine's store when this checkout is a copy, not this machine's checkout of the repo"`
}

// RunOut is the output of migrate.run.
type RunOut struct {
	DryRun bool `json:"dry_run,omitempty"`
	// Count is every row the run wrote or moved (would, under dry_run):
	// the Rows lines plus the rows counted by the settings Steps, so a
	// run that moved rows never reports 0.
	Count int      `json:"count"`
	Rows  []string `json:"rows"`
	// Steps are the settings changes (identity, store markers).
	Steps []string `json:"steps"`
	// Journal is the run's own lines: backup, resume, offline, push, undo
	// (kept apart so a dry run and a run list the same Steps).
	Journal []string `json:"journal,omitempty"`
	// BackupRef is the local backup of this run ("" for a dry run or a
	// Rewriter without Journal).
	BackupRef string `json:"backup_ref,omitempty"`
}

var _ op.Texter = RunOut{}

// Text implements op.Texter: the human form of a run, one line per item
// (FORMAT_EVOLUTION 4 example), never an empty "rows:" line. The count
// line comes first ("Upgraded N rows", "Would upgrade N rows", or
// "Nothing to migrate" when the run found nothing), then each row and
// settings step indented, then the run's own journal lines.
func (o RunOut) Text() string {
	var b strings.Builder
	verb := "Upgraded"
	if o.DryRun {
		verb = "Would upgrade"
	}
	switch {
	case o.Count == 0 && len(o.Rows) == 0 && len(o.Steps) == 0:
		b.WriteString("Nothing to migrate: every row is already in the current format.\n")
	default:
		b.WriteString(verb + " " + plural.Count(o.Count, "row") + "\n")
	}
	for _, r := range o.Rows {
		b.WriteString("  " + r + "\n")
	}
	for _, s := range o.Steps {
		b.WriteString("  " + s + "\n")
	}
	for _, j := range o.Journal {
		b.WriteString(j + "\n")
	}
	if o.DryRun {
		b.WriteString("Nothing written (dry run).\n")
	}
	return b.String()
}

// OpEnable is `lore migrate enable <feature>`, POST /v1/migrate/enable.
const OpEnable = "migrate.enable"

// EnableIn is the input of migrate.enable. Feature is required, but
// checked by the op itself (required:"after-lookup", op/required.go) so a
// missing one answers the op's own hint naming the command, not the
// decoder's bare "missing required field feature" (INTEG-MIGREST-2).
type EnableIn struct {
	Feature string `json:"feature,omitempty" required:"after-lookup" arg:"0" desc:"the feature to declare (hlc, meta, policy, resources, …; required)"`
}

// EnableOut is the output of migrate.enable.
type EnableOut struct {
	Feature string `json:"feature"`
	// Added is false when the project already declared it.
	Added          bool     `json:"added"`
	Features       []string `json:"features"`
	MinLoreVersion string   `json:"min_lore_version,omitempty"`
}

// Ops returns the migration operations bound to rw (plus status, rollback
// and unlock when rw also implements Journal).
func Ops(rw Rewriter) []op.Entry {
	out := baseOps(rw)
	if j, ok := rw.(Journal); ok {
		out = append(out, journalOps(rw, j)...)
	}
	return out
}

// run is migrate.run (FORMAT_EVOLUTION rule 4, steps 2-7): lock (M) →
// Begin (backup ref + journal, or resume) → settings → live → rewrites
// (--offline: the repo's rows only) → Finish (push unless --no-push or
// --offline). Lock order M then T (7.5); T only for a run that pushes
// (not --dry-run, --no-push or --offline: nothing reaches a teammate).
func run(ctx context.Context, rw Rewriter, in RunIn) (RunOut, error) {
	out := RunOut{DryRun: in.DryRun, Rows: []string{}, Steps: []string{}}
	if !in.DryRun { // a dry run only reads, and reading works on a gated project
		if err := rw.WriteGate(ctx); err != nil {
			return RunOut{}, err
		}
	}
	unlock, err := rw.LockMigration(ctx)
	if err != nil {
		return RunOut{}, err
	}
	defer func() { _ = unlock() }() // a failed release leaves a lock file `lore migrate unlock` clears
	if !in.DryRun {
		ctx = WithRun(ctx) // F-MIGRATE-1: this run's own writes answer the O6 question, never ask it
	}
	if in.MachineLocal {
		ctx = WithMachineLocal(ctx)
	}
	pushes := !in.DryRun && !in.NoPush && !in.Offline
	hold, err := takeTeamLock(ctx, pushes)
	if err != nil {
		return RunOut{}, err
	}
	defer func() { _ = hold.release() }() // released before M (reverse order); an unreleased hold expires (7.2)
	// teamLines are the run's team-lock lines: what taking T said (or that
	// this surface has none), and the release when T was really taken.
	// They survive a no-op run (OV38: the run says whether a team lock
	// guarded it), and a surface without T never prints a release.
	var teamLines []string
	if hold.line != "" {
		teamLines = append(teamLines, hold.line)
	}
	out.Journal = append(out.Journal, teamLines...)
	if err := At(ctx, PointTeamLocked); err != nil {
		return RunOut{}, err
	}
	j, journaled := rw.(Journal)
	if journaled && !in.DryRun {
		b, err := j.Begin(ctx, in.Offline)
		if err != nil {
			return RunOut{}, err
		}
		out.BackupRef = b.BackupRef
		out.Journal = append(out.Journal, b.Lines...)
		if err := At(ctx, PointBegun); err != nil {
			return RunOut{}, err
		}
	}
	rewrite := rw.RewriteV1
	if in.Offline && journaled {
		rewrite = j.RewriteRepoV1
		out.Journal = append(out.Journal, "offline: no team lock taken; branch files only (settings, live rows and stores skipped: run `lore migrate run` again when online)")
	} else {
		settings, err := rw.MigrateSettings(ctx, in.DryRun)
		if err != nil {
			return RunOut{}, err
		}
		out.Steps = append(out.Steps, settings.Steps...)
		out.Count += settings.Rows
		if err = At(ctx, PointSettings); err != nil {
			return RunOut{}, err
		}
		live, err := rw.MigrateLive(ctx, in.DryRun)
		if err != nil {
			return RunOut{}, err
		}
		out.Rows = append(out.Rows, live...)
		if err := At(ctx, PointLive); err != nil {
			return RunOut{}, err
		}
	}
	for _, t := range rw.MigratedTables() {
		got, err := rewrite(ctx, t, in.DryRun)
		if err != nil {
			return RunOut{}, err
		}
		for _, id := range got {
			// A v0.1.17 live row that also sits at v1 in .lore/data is one
			// file: the live step writes it as _v: 2 and the rewrite then
			// skips it, so a real run names it once. A dry run writes
			// nothing, so the rewrite still finds the file at v1; without
			// this the dry run promised "2 rows" (the same row twice) that
			// the real run then reported as 1.
			if line := t.String() + "/" + id.String(); !slices.Contains(out.Rows, line) {
				out.Rows = append(out.Rows, line)
			}
		}
		if len(got) > 0 && !in.DryRun { // a table with nothing to rewrite is no boundary
			if err := At(ctx, PointTable); err != nil {
				return RunOut{}, err
			}
		}
	}
	out.Count += len(out.Rows)
	if !in.DryRun {
		if err := At(ctx, PointFinish); err != nil {
			return RunOut{}, err
		}
	}
	if journaled && !in.DryRun {
		lines, err := j.Finish(ctx, !in.NoPush && !in.Offline, out.Count)
		if err != nil {
			return RunOut{}, err
		}
		out.Journal = append(out.Journal, lines...)
		if len(lines) == 0 { // nothing changed: the backup was dropped; the team-lock lines stay
			out.BackupRef, out.Journal = "", teamLines
		}
		if hold.taken { // a host that took no hold (no remote) never prints a release
			out.Journal = append(out.Journal, teamLockReleasedLine)
		}
	}
	return out, nil
}

// baseOps are run and enable.
func baseOps(rw Rewriter) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Rewrite v0.1.17 row files (_v: 1) as _v: 2 (the migrated nouns)",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Errors: []errs.Code{errs.Stale, errs.StoreLocked, errs.StoreReadOnly, errs.InvalidInput, errs.OldLore, errs.MigrateLocked, errs.ConfigInvalid, errs.Internal},
			Examples: []op.Example{
				{Title: "see what would change", CLI: "lore migrate run --dry-run", Want: "count:"},
				{Title: "rewrite once the whole team runs 0.1.18", CLI: "lore migrate run", Want: "count:"},
			},
		}, func(ctx context.Context, in RunIn) (RunOut, error) {
			return run(ctx, rw, in)
		}),
		// migrate.enable (FORMAT_EVOLUTION rule 2): declare → (ask while
		// teammates on v0.1.17 wrote the rows: E_NEEDS_CONFIRM, FQ2 as
		// changed by the agents override) → raise features and
		// min_lore_version in .lore/data/_meta.json. Writes no row.
		op.Define(op.Spec{
			Name: OpEnable, Summary: "Declare a format feature before its first use (raises min_lore_version)",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Confirm: op.ConfirmRule{MayAsk: true},
			Errors:  []errs.Code{errs.InvalidInput, errs.NeedsConfirm, errs.OldLore, errs.NotFound, errs.Internal},
			Examples: []op.Example{
				{Title: "declare the 0.1.18 row format once the team has upgraded", CLI: "lore migrate enable hlc --yes", Want: "feature:"},
			},
		}, func(ctx context.Context, in EnableIn) (EnableOut, error) {
			if in.Feature == "" {
				return EnableOut{}, errs.New(errs.InvalidInput, "migrate: name the feature to enable").WithHint("lore migrate enable <feature>")
			}
			return rw.EnableFeature(ctx, in.Feature)
		}),
	}
}

// Run is migrate.run over rw, for a caller that runs the migration as one
// step of a larger command (lore fix, the first write in a v0.1.17
// project): the same lock, journal, steps and push as `lore migrate run`.
func Run(ctx context.Context, rw Rewriter, in RunIn) (RunOut, error) { return run(ctx, rw, in) }
