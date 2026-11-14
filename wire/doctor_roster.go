package wire

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
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
// writer's next write to the store registers it, so for them it is a
// note that leaves the doctor status alone (D-17).
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
			held, herr := rosterHolds(ctx, st, rc.src, id)
			if herr != nil {
				return nil, herr
			}
			if !held {
				missing = append(missing, tv.Actor)
			}
		}
		for _, actor := range missing {
			who := name(actor)
			if who != actor {
				who += " (" + actor + ")"
			}
			// Another actor's rows (D-17: an agent session of a teammate)
			// are a note: no one reading this can add that row, and the
			// rows read fine; only the id shows where a name would. The
			// caller's own missing row stays a warning --fix repairs.
			f := DoctorFinding{Check: DoctorCheckActorRoster, Level: DoctorNote, Store: rc.src,
				Message: "store " + string(rc.src) + " holds rows saved by " + who + ", an actor its roster does not hold, so they show that id instead of a name",
				Fix:     "nothing to run here: that actor's next write to " + string(rc.src) + " adds it to the roster"}
			if actor == st.actor.String() {
				f.Level = DoctorWarning
				f.Fix = "lore doctor --fix (writes your actor row into " + string(rc.src) + ")"
				if fix {
					if ferr := a.EnsureActor(ctx, rc.src); ferr != nil {
						f.Message += "; adding it failed: " + ferr.Error()
					} else if id, perr := ids.Parse[ids.Actor](actor); perr == nil {
						// E-15: fixed only when the check now passes; ensureActor
						// writes the owner's row for an agent in a client store,
						// so "fixed" was printed on every run before the same
						// warning came back
						held, herr := rosterHolds(ctx, st, rc.src, id)
						switch {
						case herr != nil:
							f.Message += "; adding it failed: " + herr.Error()
						case held:
							f.Fixed = true
						default:
							f.Message += "; --fix wrote your actor row, which is not this id (an earlier identity of yours), so it cannot add it"
							f.Fix = "that actor's next write to " + string(rc.src) + " adds it to the roster; the rows read the same until then"
						}
					}
				}
			}
			out = append(out, f)
		}
	}
	return out, nil
}

// rosterHolds reports whether store src's roster accounts for actor id:
// its own actors row, or, for the caller's agent in a client store, the
// owner's row (the engine shows an agent there as its owner, P2B-OLD-2,
// and ensureActor writes the owner's row there, never the agent's).
func rosterHolds(ctx context.Context, st *state, src store.Source, id ids.ID[ids.Actor]) (bool, error) {
	want := id
	if id == st.actor && !st.agentOwner.IsZero() && st.eng.IsClient(src) {
		want = st.agentOwner
	}
	_, _, err := st.eng.Get(ctx, engine.Ref{Store: src, Table: schema.TableActors, ID: want.Any()})
	if err == nil {
		return true, nil
	}
	if errs.Has(err, errs.NotFound) {
		return false, nil
	}
	return false, err
}

var _ opsmigrate.ActorRecorder = (*App)(nil)

// RecordRunActor implements opsmigrate.ActorRecorder: after `lore migrate
// run` wrote its rows, every store holding a row the run stamped with the
// caller's actor (created_by or updated_by) gets the caller's actor row
// (ensureActor), so the migration diff carries it and doctor's
// actor_roster check stays quiet (E-3: the tag fold stamped 10 memories
// with the migrator while .lore/data/actors kept only the old actors).
// It reads the row files, not the caches: a combined cache (CFG3) cannot
// tell the stores apart. Other writers' rows are not this run's to
// register.
func (a *App) RecordRunActor(ctx context.Context) error {
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	if st.actor.IsZero() {
		return nil
	}
	me := st.actor.String()
	for i, src := range st.eng.Stores() {
		if src == store.SourceMachine || i >= len(st.backends) {
			continue // machine-local rows hold no actors table (P3-LOCAL-1)
		}
		rows, _, rerr := st.backends[i].Read(ctx, "")
		if rerr != nil {
			return errs.Prefix("migrate: read store "+string(src)+" for its writers", rerr)
		}
		if !slices.ContainsFunc(rows, func(sr store.Row) bool { return !sr.Deleted && writtenBy(sr.Doc, me) }) {
			continue
		}
		if held, herr := rosterHolds(ctx, st, src, st.actor); herr != nil || held {
			if herr != nil {
				return herr
			}
			continue
		}
		if eerr := a.EnsureActor(ctx, src); eerr != nil {
			return errs.Prefix("migrate: write your actor row into store "+string(src), eerr)
		}
	}
	return nil
}

// writtenBy reports whether a row file names actor as its creator or last
// writer (a file that is not a JSON object names nobody).
func writtenBy(doc []byte, actor string) bool {
	var w struct {
		CreatedBy string `json:"created_by"`
		UpdatedBy string `json:"updated_by"`
	}
	if json.Unmarshal(doc, &w) != nil {
		return false
	}
	return w.CreatedBy == actor || w.UpdatedBy == actor
}
