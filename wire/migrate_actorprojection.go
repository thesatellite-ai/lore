package wire

// migrate_actorprojection.go — the actor_projection step of `lore migrate
// run`: every actor copy in the code repo's .lore/data that lore itself
// would write as the {id, display name, kind} projection (fullActorRow is
// false for the repo store: a client repo, or a repo outside any org) is
// trimmed to it.
//
//	lore 0.1.17 ─▶ actor copies in the repo carry stable_key (an email)
//	lore 0.1.18 before this fix ─▶ an actor copy it created carried `_o`
//	migrate run ─▶ step actor_projection: each such copy loses exactly the
//	               keys the client-repo guard and `lore check leaks --ci`
//	               refuse (leakcheck.ActorCopyExtraKeys), so the migrated
//	               files commit, push and pass CI
//
// Why: the run's last line told the admin to commit the migration, and the
// commit was then refused by the client-repo guard (stable_key), or the
// CI check failed every repo, with no lore command that fixed the files
// (user report). The step is the one command those refusals now name
// (leakcheck.ActorCopyFix); it is safe to run again on a migrated repo
// (a second run finds nothing to trim and prints nothing). An org's own
// repo keeps stable_key (Q-R2-2: fullActorRow), so it is left as it is.
//
// A v1 copy is upcast as the format rewrite would (upcastV1) before it is
// trimmed, so the rewrite that follows finds it already in format 2.

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// StepActorProjection names the step as the run reports it.
const StepActorProjection = "actor_projection"

// migrateActorProjection is the step: it returns the step lines and the
// copies trimmed (dry run: the copies it would trim), each also named in
// Upgraded so the run counts a copy it trims and rewrites as one row (E-2).
func (a *App) migrateActorProjection(ctx context.Context, dryRun bool) (opsmigrate.Settings, error) {
	st, err := a.state(ctx)
	if err != nil {
		return opsmigrate.Settings{}, err
	}
	if fullActorRow(st, store.SourceRepo) {
		return opsmigrate.Settings{}, nil // an org's own repo keeps the full actor row (Q-R2-2)
	}
	names := st.eng.Stores()
	at := -1
	for i, n := range names {
		if n == store.SourceRepo {
			at = i
		}
	}
	if at < 0 || at >= len(st.backends) {
		return opsmigrate.Settings{}, nil // no repo store attached: nothing to trim
	}
	b := st.backends[at]
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return opsmigrate.Settings{}, errs.Prefix("migrate: read the repo's actor copies", err)
	}
	reg, err := builtinRegistry()
	if err != nil {
		return opsmigrate.Settings{}, err
	}
	var changes []store.Change
	var trimmed []string
	keys := map[string]bool{}
	for _, sr := range rows {
		if sr.Table != schema.TableActors || sr.Deleted {
			continue
		}
		r, derr := sr.Decode()
		if derr != nil {
			continue // a broken row file is the format step's report
		}
		if r.SourceVersion != row.FormatVersion {
			r = upcastV1(reg, r)
		}
		up, ferr := store.FromRow(r)
		if ferr != nil {
			return opsmigrate.Settings{}, ferr
		}
		extra := leakcheck.ActorCopyExtraKeys(up.Doc)
		if len(extra) == 0 {
			continue
		}
		for _, k := range extra {
			keys[k] = true
		}
		up, ferr = store.FromRow(trimActorCopy(r, extra))
		if ferr != nil {
			return opsmigrate.Settings{}, ferr
		}
		changes = append(changes, store.Change{Row: up, Expect: sr.HLC})
		trimmed = append(trimmed, string(schema.TableActors)+"/"+sr.ID.String())
	}
	if len(changes) == 0 {
		return opsmigrate.Settings{}, nil
	}
	verb := "trimmed"
	if dryRun {
		verb = "would trim"
	}
	line := StepActorProjection + ": " + verb + " " + copiesText(len(changes)) + " in .lore/data to {id, display name, kind} (dropped " + strings.Join(slices.Sorted(maps.Keys(keys)), ", ") + ")"
	done := opsmigrate.Settings{Steps: []string{line}, Rows: len(changes), Upgraded: trimmed}
	if dryRun {
		return done, nil
	}
	if werr := b.Write(ctx, changes); werr != nil {
		if errs.Has(werr, errs.StoreReadOnly) {
			return opsmigrate.Settings{Steps: []string{StepActorProjection + ": the repo store is read-only here: " + strconv.Itoa(len(changes)) + " actor copies not trimmed"}}, nil
		}
		return opsmigrate.Settings{}, errs.Prefix("migrate: trim the repo's actor copies", werr)
	}
	a.wrote(nil)
	if _, err := st.eng.Refresh(ctx, store.SourceRepo); err != nil {
		return opsmigrate.Settings{}, err
	}
	return done, nil
}

// trimActorCopy returns r without the document keys extra (as
// leakcheck.ActorCopyExtraKeys names them): an envelope key clears its
// struct field, any other key is a field or a reserved key.
func trimActorCopy(r row.Row, extra []string) row.Row {
	fields := make(map[string]value.Value, len(r.Fields))
	for k, v := range r.Fields {
		fields[k] = v
	}
	reserved := make(map[string]value.Value, len(r.Reserved))
	for k, v := range r.Reserved {
		reserved[k] = v
	}
	for _, k := range extra {
		switch k {
		case row.KeyOrigin:
			r.Origin = value.JSON{}
		case row.KeyMeta:
			r.Meta = nil
		case row.KeyCreatedBy:
			r.CreatedBy = ids.ID[ids.Actor]{}
		case row.KeyUpdatedBy:
			r.UpdatedBy = ids.ID[ids.Actor]{}
		default:
			delete(fields, k)
			delete(reserved, k)
		}
	}
	r.Fields, r.Reserved = fields, reserved
	return r
}
