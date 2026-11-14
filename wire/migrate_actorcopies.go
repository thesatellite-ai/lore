package wire

// migrate_actorcopies.go — the actor_copies step of `lore migrate run`:
// every actor copy that differs from its source is rewritten from it, so
// the first doctor after the upgrade has nothing to flag.
//
//	lore 0.1.17, no identity configured ─▶ the actor row it wrote is named
//	                                      by the email's local part ("una")
//	lore 0.1.18 ─▶ the git identity shows git's user.name ("Una Upgrader",
//	               owner decision 2, 2026-11-10): the old copy is stale
//	migrate run ─▶ step actor_copies: each stale copy refreshed through
//	               the source-of-truth path (refreshCopy, as `lore actor
//	               refresh`), one line naming the actors
//
// Before this step the copy stayed stale until the person's next write to
// that store or `lore actor refresh`, and doctor warned in between. The
// step is best effort per copy: a copy that cannot be written (a read-only
// store) is named in the step line and left for the doctor; it never fails
// the run. A dry run names what it would refresh and writes nothing. A
// second run finds nothing stale and prints nothing (F-MIGRATE-1).

import (
	"context"
	"slices"
	"strconv"
	"strings"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// StepActorCopies names the step as the run reports it.
const StepActorCopies = "actor_copies"

// migrateActorCopies is the step: it returns the step lines, the copies
// written (dry run: the copies it would write) as Rows, and each copy in
// the repo store named in Upgraded ("actors/<id>"). Named, a copy counts
// once with the format rewrite: a dry run finds the copy still at `_v: 1`
// and the rewrite lists it too, while the real run refreshes it first and
// the rewrite then skips it; unnamed, the dry run promised one actor row
// more than the real run reported (devco K-5).
func (a *App) migrateActorCopies(ctx context.Context, dryRun bool) (opsmigrate.Settings, error) {
	open := a.writeState
	if dryRun {
		open = a.state // a dry run only reads
	}
	st, err := open(ctx)
	if err != nil {
		return opsmigrate.Settings{}, err
	}
	all, err := a.actorIDs(ctx, st)
	if err != nil {
		return opsmigrate.Settings{}, err
	}
	var names, failed, upgraded []string
	n := 0
	for _, id := range all {
		rep := a.actorReport(ctx, st, id)
		stale := rep.Stale()
		if len(stale) == 0 {
			continue
		}
		done := 0
		for _, c := range stale {
			if _, _, _, differs := firstDiff(rep.Source, c); !differs {
				continue // only the stamp differs: nothing a reader sees is wrong (doctor_actorcopies.go)
			}
			repoFile := string(schema.TableActors) + "/" + id.String()
			if dryRun {
				done++
				if c.Store == string(store.SourceRepo) {
					upgraded = append(upgraded, repoFile)
				}
				continue
			}
			wrote, rerr := refreshCopy(ctx, st, id, rep.Source, c)
			a.wroteTo(store.Source(c.Store), rerr)
			if rerr != nil {
				failed = append(failed, actorShownName(rep, id)+" in "+c.Store+" ("+rerr.Error()+")")
				continue
			}
			if wrote {
				done++
				if c.Store == string(store.SourceRepo) {
					upgraded = append(upgraded, repoFile)
				}
			}
		}
		if done > 0 {
			n += done
			names = append(names, actorShownName(rep, id))
		}
	}
	var lines []string
	if n > 0 {
		verb := "refreshed"
		if dryRun {
			verb = "would refresh"
		}
		slices.Sort(names)
		lines = append(lines, StepActorCopies+": "+verb+" "+copiesText(n)+" from their source: "+strings.Join(names, ", "))
	}
	if len(failed) > 0 {
		lines = append(lines, StepActorCopies+": could not refresh "+strings.Join(failed, "; ")+"; lore doctor names them, `"+actorRefreshFix+"` retries")
	}
	return opsmigrate.Settings{Steps: lines, Rows: n, Upgraded: upgraded}, nil
}

// copiesText is "1 actor copy" / "2 actor copies".
func copiesText(n int) string {
	if n == 1 {
		return "1 actor copy"
	}
	return strconv.Itoa(n) + " actor copies"
}
