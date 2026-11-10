package wire

import (
	"context"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The actor_periods step of `lore migrate run` (TEAM_PROFILES_DESIGN 8,
// TP-22): every human actor of the org roster store that has no
// actor_periods row gets the period teamprofile.PeriodsOf synthesises from
// its active/left_at (type employee, started_on the actor row's created_at
// day, ended_on the left_at day when it left, source migrated). Nothing is
// lost: active and left_at stay on the actor, and the period carries the
// same dates readers already showed.
//
//	repoRoster ──▶ no org (repo roster): skipped, TS-1 (profile rows never
//	               go to the committed repo store; reads still synthesise)
//	           ──▶ roster store not attached here / client / read-only: one line, skipped
//	           ──▶ per human actor with no period row ──▶ Create (natural id
//	               (actor_id, started_on), TS-5)
//
// Only rows v0.1.17 wrote are materialised (#22d): an actor row written by
// 0.1.18 (`lore org create`, `org link`, `org import`, register) carries
// the `_o` provenance block every 0.1.18 create stamps (engine originOf),
// while a v0.1.17 row never has one (its v1 → v2 rewrite adds none, and an
// update keeps the create's `_o`). Such a row is skipped: its period is the
// same one PeriodsOf synthesises at read time, and the first period write
// (offboard, rejoin, period add) materialises it, so nothing is lost and a
// fresh 0.1.18 org needs no migrate step. Chosen over making org create
// write the period row so org create's writes stay as they are.
//
// Run twice, or on a second clone: the first run's rows exist, PeriodsOf
// returns them and nothing is synthesised, so the second run writes
// nothing (F-MIGRATE-1); two clones racing write the same natural id and
// merge.

// StepActorPeriods names the step as the run reports it.
const StepActorPeriods = "actor_periods"

// migrateActorPeriods is the step. It returns the step lines and the rows
// written (dry run: the rows it would write).
func (a *App) migrateActorPeriods(ctx context.Context, dryRun bool) ([]string, int, error) {
	r, err := a.repoRoster(ctx)
	if err != nil {
		return nil, 0, err
	}
	if r.src == store.SourceRepo {
		return nil, 0, nil
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	switch {
	case !slices.Contains(st.eng.Stores(), r.src):
		return []string{StepActorPeriods + ": the roster store " + string(r.src) + " is not attached here; run it in a repo of the org"}, 0, nil
	case st.eng.IsClient(r.src):
		return nil, 0, nil // never: a client store holds no roster (TP-7)
	case st.eng.ReadOnly(r.src):
		return []string{StepActorPeriods + ": the roster store " + string(r.src) + " is read-only here; periods are read from active/left_at"}, 0, nil
	}
	actors, err := st.eng.List(ctx, r.src, schema.TableActors)
	if err != nil {
		return nil, 0, errs.Prefix("migrate: "+StepActorPeriods+": reading the actors of "+string(r.src), err)
	}
	periods, err := st.eng.List(ctx, r.src, schema.TableActorPeriods)
	if err != nil {
		return nil, 0, errs.Prefix("migrate: "+StepActorPeriods+": reading the periods of "+string(r.src), err)
	}
	live := periods[:0:0]
	for _, p := range periods {
		if !p.Deleted {
			live = append(live, p)
		}
	}
	n := 0
	for _, x := range actors {
		if x.Deleted || !x.Origin.IsZero() {
			continue // gone, or written by 0.1.18 (read-time synthesis covers it, #22d)
		}
		ps, perr := teamprofile.PeriodsOf(x, live)
		if perr != nil {
			return nil, 0, errs.Prefix("migrate: "+StepActorPeriods+": actor "+x.ID.String(), perr)
		}
		if len(ps) != 1 || !ps[0].Synthesised() {
			continue // has rows, or not a person (no period)
		}
		if !dryRun {
			if werr := a.writeMigratedPeriod(ctx, st, r.src, x, ps[0]); werr != nil {
				return nil, 0, werr
			}
		}
		n++
	}
	if n == 0 {
		return nil, 0, nil
	}
	verb := "wrote"
	if dryRun {
		verb = "would write"
	}
	return []string{StepActorPeriods + ": " + verb + " " + strconv.Itoa(n) + " company period(s) from active/left_at in " + r.shown()}, n, nil
}

// writeMigratedPeriod creates the period row of actor x in src.
func (a *App) writeMigratedPeriod(ctx context.Context, st *state, src store.Source, x row.Row, p teamprofile.Period) error {
	f, err := p.Fields()
	if err != nil {
		return err
	}
	_, err = st.eng.Create(ctx, src, schema.TableActorPeriods, f)
	a.wroteTo(src, err)
	if err != nil {
		return errs.Prefix("migrate: "+StepActorPeriods+": the period of actor "+x.ID.String(), err)
	}
	return nil
}
