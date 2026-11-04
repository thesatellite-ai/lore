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
package migrate

import (
	"context"

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
}

// OpEnable is `lore migrate enable <feature>`, POST /v1/migrate/enable.
const OpEnable = "migrate.enable"

// EnableIn is the input of migrate.enable.
type EnableIn struct {
	Feature string `json:"feature" arg:"0" desc:"the feature to declare (hlc, meta, policy, resources, …; required)"`
}

// EnableOut is the output of migrate.enable.
type EnableOut struct {
	Feature string `json:"feature"`
	// Added is false when the project already declared it.
	Added          bool     `json:"added"`
	Features       []string `json:"features"`
	MinLoreVersion string   `json:"min_lore_version,omitempty"`
}

// Ops returns the migration operations bound to rw.
func Ops(rw Rewriter) []op.Entry {
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
			out := RunOut{DryRun: in.DryRun, Rows: []string{}, Steps: []string{}}
			unlock, err := rw.LockMigration(ctx)
			if err != nil {
				return RunOut{}, err
			}
			defer func() { _ = unlock() }() // a failed release leaves a lock file `lore migrate run` reports next time
			settings, err := rw.MigrateSettings(ctx, in.DryRun)
			if err != nil {
				return RunOut{}, err
			}
			out.Steps = append(out.Steps, settings.Steps...)
			live, err := rw.MigrateLive(ctx, in.DryRun)
			if err != nil {
				return RunOut{}, err
			}
			out.Rows = append(out.Rows, live...)
			for _, t := range rw.MigratedTables() {
				got, err := rw.RewriteV1(ctx, t, in.DryRun)
				if err != nil {
					return RunOut{}, err
				}
				for _, id := range got {
					out.Rows = append(out.Rows, t.String()+"/"+id.String())
				}
			}
			out.Count = len(out.Rows) + settings.Rows
			return out, nil
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
