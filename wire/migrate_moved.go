package wire

// migrate_moved.go — rows a newer lore moved out of this repo whose old
// copy is still here (lore fix: E_AMBIGUOUS_ID "msn_… is in repo and
// acme/org", and the repo could never be migrated).
//
// A move writes the row at its new place (with a `moves` entry in _o) and
// then a tombstone where it was. When the move ran in another checkout of
// the repo (or its tombstone was never committed), this checkout still
// holds the old copy: a v0.1.17 file in .lore/data, a row lore.db brought
// in, or a v0.1.17 live-ref row. Every lookup of that id is then
// E_AMBIGUOUS_ID, and the migration stopped on it.
//
// What 0.1.17 meant is clear from the moved copy: the row was moved, so
// the store holding the copy with a move record owns it. The step does
// what the move would have finished doing, never duplicating or dropping:
//
//	repo copy older (or the same) ─▶ tombstone it here (the move's own
//	                                 second half)
//	repo copy newer (edited here   ─▶ merge it into the owner's copy
//	after the move, by 0.1.17)         (logic/merge, newer field wins),
//	                                   then tombstone it here
//	live-ref row (never in .lore/  ─▶ not brought into the repo (pending
//	data)                              LiveRows skips it); a newer one is
//	                                   merged into the owner the same way
//
// Each row is one step line naming the owner, so the run records what it
// did. A row two stores hold with no move record is left as it is (no
// owner can be told): the E_AMBIGUOUS_ID it then gives names the command
// that settles it (lore row move <id> --to <store>).

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// movedStepPrefix starts every step line of the moved-rows step.
const movedStepPrefix = "moved rows: "

// movedOwner is the store that owns a row this repo still holds a copy of.
type movedOwner struct {
	src store.Source
	cur row.Row
}

// movedOwnerOf is the attached store (not the repo) holding a live copy of
// table/id that arrived there by a move (its _o has a moves entry); ok is
// false when no store does, or more than one does. A store that cannot be
// read is passed over: the step must not break the migration on a store
// that is offline.
func movedOwnerOf(ctx context.Context, st *state, table row.Table, id ids.AnyID) (movedOwner, bool) {
	if table == schema.TableActors {
		return movedOwner{}, false // an actor's copies are one person (engine newestCopy), never ambiguous
	}
	var found []movedOwner
	for _, name := range st.eng.Stores() {
		b := st.byName[name]
		if name == store.SourceRepo || b == nil {
			continue
		}
		sr, err := b.Get(ctx, table, id)
		if err != nil || sr.Deleted {
			continue
		}
		cur, err := sr.Decode()
		if err != nil || !hasMoveRecord(cur) {
			continue
		}
		found = append(found, movedOwner{src: name, cur: cur})
	}
	if len(found) != 1 {
		return movedOwner{}, false
	}
	return found[0], true
}

// hasMoveRecord reports whether r's _o holds a non-empty moves list.
func hasMoveRecord(r row.Row) bool {
	if r.Origin.IsZero() {
		return false
	}
	ms, err := canon.Members(r.Origin.Raw())
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.Key != originMovesKey {
			continue
		}
		list, lerr := canon.Elements(m.Raw)
		return lerr == nil && len(list) > 0
	}
	return false
}

// originMovesKey is the `moves` list in a row's _o (engine/move.go).
const originMovesKey = "moves"

// migrateMovedLeftovers is the moved-rows step of `lore migrate run` (see
// the file comment): one line per row, the rows counted; dryRun writes
// nothing.
func (a *App) migrateMovedLeftovers(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	repo := st.byName[store.SourceRepo]
	if repo == nil {
		return nil, 0, nil
	}
	rows, _, err := repo.Read(ctx, "")
	if err != nil {
		return nil, 0, errs.Prefix("migrate: read the repo's rows", err)
	}
	var steps []string
	var tombs []store.Change
	ownerWrites := map[store.Source][]store.Change{}
	var owners []store.Source
	for _, sr := range rows {
		if sr.Deleted {
			continue
		}
		own, ok := movedOwnerOf(ctx, st, sr.Table, sr.ID)
		if !ok {
			continue
		}
		here, derr := sr.Decode()
		if derr != nil {
			return nil, 0, derr
		}
		line := movedStepPrefix + sr.Table.String() + "/" + sr.ID.String() + " is in " + string(own.src) + " (moved there by a newer lore); "
		if here.HLC.Compare(own.cur.HLC) > 0 {
			merged, merr := mergeRow(own.cur, here)
			if merr != nil {
				return nil, 0, merr
			}
			up, ferr := store.FromRow(merged)
			if ferr != nil {
				return nil, 0, ferr
			}
			if _, seen := ownerWrites[own.src]; !seen {
				owners = append(owners, own.src)
			}
			ownerWrites[own.src] = append(ownerWrites[own.src], store.Change{Row: up, Expect: own.cur.HLC})
			line += "this repo's copy was edited after the move, so its changes were merged into " + string(own.src) + " and the copy here is now a tombstone"
		} else {
			line += "this repo's older copy is now a tombstone, as the move leaves it"
		}
		h := hlc.Next(hlc.Max(sr.HLC, own.cur.HLC), time.Now(), sr.HLC.Actor)
		tomb, terr := store.FromRow(row.Row{Table: sr.Table, ID: sr.ID, HLC: h, Deleted: true, UpdatedAt: h.Wall, UpdatedBy: sr.HLC.Actor})
		if terr != nil {
			return nil, 0, terr
		}
		tombs = append(tombs, store.Change{Row: tomb, Expect: sr.HLC})
		steps = append(steps, line)
	}
	if dryRun || len(tombs) == 0 {
		return steps, len(steps), nil
	}
	// the owner first: a run stopped between the two writes leaves the row
	// in both places, and the next run finds it and finishes (as a move run
	// again only writes the tombstone)
	for _, name := range owners {
		if err := st.byName[name].Write(ctx, ownerWrites[name]); err != nil {
			return nil, 0, errs.Prefix("migrate: merge moved rows into "+string(name), err)
		}
		if _, err := st.eng.Sync(ctx, name); err != nil {
			return nil, 0, errs.Prefix("migrate: push the moved rows of "+string(name), err)
		}
	}
	if err := repo.Write(ctx, tombs); err != nil {
		return nil, 0, errs.Prefix("migrate: leave tombstones of moved rows in .lore/data", err)
	}
	a.wrote(nil)
	if _, err := st.eng.Refresh(ctx, store.SourceRepo); err != nil {
		return nil, 0, err
	}
	return steps, len(steps), nil
}

// movedLiveLine is the step line of a v0.1.17 live-ref row that a newer
// lore moved to another store: it is not brought into the repo.
func movedLiveLine(table row.Table, id ids.AnyID, owner store.Source, merged bool) string {
	line := movedStepPrefix + table.String() + "/" + id.String() + " (lore 0.1.17 live ref) is in " + string(owner) + " (moved there by a newer lore); "
	if merged {
		return line + "the live copy was newer, so its changes were merged there"
	}
	return line + "the older live copy is left out"
}

// isAmbiguousMoved reports whether err is E_AMBIGUOUS_ID for a row the
// moved-rows step settles (a dry run, which writes nothing, still finds
// both copies).
func isAmbiguousMoved(ctx context.Context, st *state, err error, table row.Table, id ids.AnyID) bool {
	if !errs.Has(err, errs.AmbiguousID) {
		return false
	}
	_, ok := movedOwnerOf(ctx, st, table, id)
	return ok
}
