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
	// MigrateSettings converts the v0.1.17 settings files (identity.yaml,
	// v1 store markers) and returns one line per change (made, or that
	// would be made under dryRun).
	MigrateSettings(ctx context.Context, dryRun bool) ([]string, error)
	// LockMigration takes the migration lock (E_MIGRATE_LOCKED when held);
	// the returned func releases it.
	LockMigration(ctx context.Context) (func() error, error)
}

// RunIn is the input of migrate.run.
type RunIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list the files that would be rewritten; change nothing"`
}

// RunOut is the output of migrate.run.
type RunOut struct {
	DryRun bool     `json:"dry_run,omitempty"`
	Count  int      `json:"count"`
	Rows   []string `json:"rows"`
	// Steps are the settings changes (identity, store markers).
	Steps []string `json:"steps"`
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
			steps, err := rw.MigrateSettings(ctx, in.DryRun)
			if err != nil {
				return RunOut{}, err
			}
			out.Steps = append(out.Steps, steps...)
			for _, t := range rw.MigratedTables() {
				got, err := rw.RewriteV1(ctx, t, in.DryRun)
				if err != nil {
					return RunOut{}, err
				}
				for _, id := range got {
					out.Rows = append(out.Rows, t.String()+"/"+id.String())
				}
			}
			out.Count = len(out.Rows)
			return out, nil
		}),
	}
}
