package wire

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckActorRoster is the "rows saved by an actor the store's roster
// does not hold" check of `lore doctor` (FB-ACTOR-ROSTER).
const DoctorCheckActorRoster DoctorCheck = "actor_roster"

// rosterCache is one store and the cache file holding only its rows.
type rosterCache struct {
	src   store.Source
	cache *sqlite.Cache
}

// rosterCaches are the attached stores with a cache of their own. The
// machine store is left out (its machine-local rows never travel, and
// actors is not one of its tables: P3-LOCAL-1; doctor names their writers
// from this machine's identities), and so is the CFG3 combined cache,
// whose rows cannot be told apart by store.
func (c *caches) rosterCaches() []rosterCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []rosterCache
	for _, src := range c.order {
		x := c.by[src]
		if src == store.SourceMachine || x == nil || x == c.combined {
			continue
		}
		out = append(out, rosterCache{src: src, cache: x})
	}
	return out
}

// DoctorActorRoster warns about each store holding saved rows whose
// writer (updated_by, else created_by, of a 0.1.18-stamped row) is not an
// actor row of that same store: every write lands the writer's actor row
// in the store it writes to first (ensureActor), so such a row was
// written around that path, and a teammate reading the store sees a bare
// act_ id. When the writer is the caller, `lore doctor --fix` writes the
// caller's actor row there (the write path's own ensureActor); any other
// writer's next write to the store registers it.
func (a *App) DoctorActorRoster(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	name := a.teamActorNamer(ctx)
	var out []DoctorFinding
	for _, rc := range st.caches.rosterCaches() {
		tvs, terr := rc.cache.TeamVersions(ctx)
		if terr != nil {
			return nil, terr
		}
		var missing []string
		for _, tv := range tvs {
			if tv.Actor == "" || slices.Contains(missing, tv.Actor) {
				continue
			}
			id, perr := ids.Parse[ids.Actor](tv.Actor)
			if perr != nil {
				missing = append(missing, tv.Actor) // not an actor id at all: no roster can hold it
				continue
			}
			if _, _, gerr := st.eng.Get(ctx, engine.Ref{Store: rc.src, Table: schema.TableActors, ID: id.Any()}); gerr == nil {
				continue
			} else if !errs.Has(gerr, errs.NotFound) {
				return nil, gerr
			}
			missing = append(missing, tv.Actor)
		}
		for _, actor := range missing {
			f := DoctorFinding{Check: DoctorCheckActorRoster, Level: DoctorWarning, Store: rc.src,
				Message: "store " + string(rc.src) + " holds rows saved by " + name(actor) + " (" + actor + "), an actor its roster does not hold",
				Fix:     "that actor's next write to " + string(rc.src) + " adds it to the roster"}
			if actor == st.actor.String() {
				f.Fix = "lore doctor --fix (writes your actor row into " + string(rc.src) + ")"
				if fix {
					if ferr := a.EnsureActor(ctx, rc.src); ferr != nil {
						f.Message += "; adding it failed: " + ferr.Error()
					} else {
						f.Fixed = true
					}
				}
			}
			out = append(out, f)
		}
	}
	return out, nil
}
