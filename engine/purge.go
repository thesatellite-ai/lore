package engine

// Purge (DELETION_DESIGN 3.5, STORE_SERVER_SPEC 6.1): removing a row for
// good. The core's delete already writes a tombstone kept forever (DQ4); a purge
// also lists the id in the folder's .lore/data/_purged.json, which
// v0.1.17 honours on ADOPT and git merges as a set union, and which the
// engine reads before every write: a purged id never holds a live row
// again (put refuses it, a natural-key create takes a fresh id).

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Purger is the optional backend upgrade a purge needs: the store keeps a
// list of ids purged on purpose (the rowfs folder and git backends: the
// folder's _purged.json). A backend without it cannot be purged in
// (E_UNSUPPORTED) and is never checked for purged ids.
type Purger interface {
	// WritePurge writes changes like Backend.Write and, in the same
	// all-or-none batch, adds purged to the list. changes may be empty
	// (the ids' tombstones are already written).
	WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error
	// Purged returns the listed ids (empty when none). A list that cannot
	// be read is an error, never an empty set.
	Purged(ctx context.Context) (map[string]bool, error)
}

// Purge removes the row ref names for good: a live row is deleted as
// Delete does (relation rules, cascades; every tombstoned id is purged;
// its comments and links, live or trashed, are purged with it,
// INTEG-TRASH-6),
// a row already deleted (a tombstone) has only its id listed. Purging an
// id already listed is a no-op that still succeeds (run twice = once),
// except that a live file an old clone put back over the tombstone
// (Resurrected) gets the tombstone written over it again.
func (e *Engine) Purge(ctx context.Context, ref Ref) (Written, error) {
	src, cur, resurrected, err := e.locateRaw(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	// DELETION 3.5: the purge commit carries the Lore-Purge trailer
	ctx = withPurgeTrailer(ctx, e.clock.Now(hlc.HLC{}).Wall, "")
	if err = refuseActorDelete(cur); err != nil { // BATCH2 #8
		return Written{}, err
	}
	st, err := e.writable(ctx, src)
	if err != nil {
		return Written{}, err
	}
	p, ok := st.Backend.(Purger)
	if !ok {
		return Written{}, errs.New(errs.Unsupported, "store "+string(src)+" cannot record purged ids")
	}
	if !cur.Deleted {
		return e.remove(ctx, src, cur, true)
	}
	if !resurrected { // a trashed row reads as a tombstone: purge its file for real (DELETION 3.5)
		raw, ok, terr := e.rawTrashed(ctx, src, cur.Table, cur.ID)
		if terr != nil {
			return Written{}, terr
		}
		if ok {
			return e.remove(ctx, src, raw, true)
		}
	}
	cl := e.callOf(ctx)
	var changes []store.Change
	var docs []store.Row
	if resurrected {
		// the live file an old clone put back: write the tombstone over it
		// again (Expect = the file's HLC, which the hidden row kept)
		next, _ := e.applyStep(step{row: cur}, e.actorOf(cl))
		if err := e.refuseNeverClient(st, next.Table); err != nil {
			return Written{}, err // TS-3
		}
		sr, ferr := store.FromRow(e.forStore(st, next))
		if ferr != nil {
			return Written{}, ferr
		}
		changes, docs, cur = []store.Change{{Row: sr, Expect: cur.HLC}}, []store.Row{sr}, next
	}
	// FORMAT_EVOLUTION rule 2: a purge is a write too, so a lore below the
	// folder's floor (or missing one of its features) is refused here.
	if err = e.gate(ctx, st, cur); err != nil {
		return Written{}, err
	}
	ev := Event{Op: cl.op, Action: ActionPurge, Store: src, Table: cur.Table, ID: cur.ID, HLC: cur.HLC, Actor: e.actorOf(cl)}
	pre := absorbStaged(ctx, src)
	bc, bev := pre.withDirect(changes, []Event{ev})
	if err = e.writePurge(ctx, st, p, bc, []ids.AnyID{cur.ID}, bev); err != nil { // later 542
		return Written{}, err
	}
	fd, fev := pre.finishDirect(docs, []Event{ev})
	aw, err := e.finishW(unstaged(ctx), src, fd, fev...) // written directly (with the stage's part for this store, absorbStaged): the cache and audit follow now
	w := Written{Row: cur, Warnings: aw}
	if err != nil {
		return w, err
	}
	// INTEG-TRASH-6: a row deleted for good earlier (a tombstone) still
	// takes the comments and links left on it, each purged as `lore purge <id>`.
	cs, err := e.purgedWith(ctx, cur.Table, cur.ID)
	if err != nil {
		return w, err
	}
	for _, c := range cs {
		cw, cerr := e.remove(ctx, c.source, c.row, true)
		w.Warnings = append(w.Warnings, cw.Warnings...)
		if cerr != nil {
			return w, cerr
		}
		w.Also = append(w.Also, Changed{Store: c.source, Row: cw.Row})
		w.Also = append(w.Also, cw.Also...)
	}
	return w, nil
}

// purgedDay is how an E_PURGED message prints the purge date
// (DELETION_DESIGN 2.7: "purged by mira on 2026-08-19").
const purgedDay = "2006-01-02"

// refusePurged fails with E_PURGED when id is listed as purged in st: a
// purged id is never used again (DELETION_DESIGN DQ4). A tombstone write
// is allowed (it keeps the row gone). Backends without the list pass.
func refusePurged(ctx context.Context, st Attached, table row.Table, id ids.AnyID, deleted bool) error {
	if deleted {
		return nil
	}
	set, err := purgedSet(ctx, st)
	if err != nil {
		return err
	}
	if set[id.String()] {
		return purgedErr(ctx, st, table, id)
	}
	return nil
}

// purgedErr is the E_PURGED error for id in st, shaped like
// DELETION_DESIGN 2.7: "<id> was purged by <actor> on <date>; purged
// rows never come back (DQ4)". Who and when come from the id's tombstone;
// when there is none (its file is gone, or an old clone put the live file
// back over it) the message names the id alone, never a guess.
func purgedErr(ctx context.Context, st Attached, table row.Table, id ids.AnyID) error {
	what := id.String() + " was purged"
	if sr, err := st.Backend.Get(ctx, table, id); err == nil && sr.Deleted {
		if r, derr := sr.Decode(); derr == nil {
			if !r.UpdatedBy.IsZero() {
				what += " by " + purgerName(ctx, st, r.UpdatedBy)
			}
			if !r.UpdatedAt.IsZero() {
				what += " on " + r.UpdatedAt.UTC().Format(purgedDay)
			}
		}
	}
	return errs.New(errs.Purged, ""+what+" from store "+string(st.Name)+"; purged rows never come back (DQ4)").
		WithHint("read what it held: lore trash show --id " + id.String() + " --from-history; then write what you still need as a new row (a new id)")
}

// purgerName is how a person reads who purged a row (DELETION_DESIGN 2.7
// "purged by mira"): the actor's display name from the store's actors
// rows, else the act_ id when the row cannot be read (never a guess).
func purgerName(ctx context.Context, st Attached, actor ids.ID[ids.Actor]) string {
	if name, ok := auditfill.NameOf(actor.String()); ok {
		return name
	}
	sr, err := st.Backend.Get(ctx, schema.TableActors, actor.Any())
	if err != nil || sr.Deleted {
		return actor.String()
	}
	r, err := sr.Decode()
	if err != nil {
		return actor.String()
	}
	if v := r.Fields[schema.FieldActorDisplayName]; v != nil && v.String() != "" {
		return v.String()
	}
	return actor.String()
}

// deletedErr is the refusal of a write to the tombstone cur of st:
// E_PURGED when its id is purged (DELETION_DESIGN 3.6: purged rows never
// come back), else E_NOT_FOUND "<id> <what>".
func deletedErr(ctx context.Context, st Attached, cur row.Row, what string) error {
	if sr, gerr := st.Backend.Get(ctx, cur.Table, cur.ID); gerr == nil && !sr.Deleted {
		if r, derr := sr.Decode(); derr == nil && trash.Is(r) {
			return inTrashErr(r) // DELETION 3.2: a write to a trashed row says where it is
		}
	}
	set, err := purgedSet(ctx, st)
	if err != nil {
		return err
	}
	if set[cur.ID.String()] {
		return purgedErr(ctx, st, cur.Table, cur.ID)
	}
	return errs.New(errs.NotFound, ""+cur.ID.String()+" "+what)
}

// purgedSet returns the ids st lists as purged; nil for a backend that
// keeps no list (it cannot be purged in).
func purgedSet(ctx context.Context, st Attached) (map[string]bool, error) {
	p, ok := st.Backend.(Purger)
	if !ok {
		return nil, nil
	}
	return p.Purged(ctx)
}

// hidePurged is sr as the engine reads it: a LIVE row whose id is listed
// as purged (an old clone, or a hand copy, put the file back) reads as a
// tombstone with no content, so no read (Get, List, FindByKey, the cache
// and so search, sql and render) ever returns a purged row again (DQ4).
// The tombstone keeps the file's HLC so a write over it (Purge repairing
// the file) still passes the backend's Expect check. resurrected reports
// that sr was hidden; `lore doctor` names such files (Resurrected).
func hidePurged(sr store.Row, set map[string]bool) (out store.Row, resurrected bool, err error) {
	if sr.Deleted || !set[sr.ID.String()] {
		return sr, false, nil
	}
	tomb, err := store.FromRow(row.Row{Table: sr.Table, ID: sr.ID, HLC: sr.HLC, Deleted: true, UpdatedAt: sr.HLC.Wall})
	if err != nil {
		return store.Row{}, false, err
	}
	return tomb, true, nil
}

// getRow is Backend.Get of store name with purged rows hidden
// (hidePurged). Every engine read of one row goes through it.
func (e *Engine) getRow(ctx context.Context, name store.Source, table row.Table, id ids.AnyID) (store.Row, bool, error) {
	st := e.stores[name]
	sr, err := st.Backend.Get(ctx, table, id)
	if err != nil || sr.Deleted {
		return sr, false, err
	}
	set, err := purgedSet(ctx, st)
	if err != nil {
		return store.Row{}, false, err
	}
	sr, res, err := hidePurged(sr, set)
	if err != nil || res {
		return sr, res, err
	}
	sr, err = hideTrashed(sr) // DELETION 3.2: a trashed row reads as gone
	return sr, false, err
}

// readRows is Backend.Read of store name with purged rows hidden
// (hidePurged): a resurrected file reaches the cache as a tombstone, so
// the cache drops any copy it had. Every engine read of many rows goes
// through it.
func (e *Engine) readRows(ctx context.Context, name store.Source, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	st := e.stores[name]
	var rows []store.Row
	var next store.Cursor
	var err error
	if cursor == "" {
		rows, next, err = readAll(ctx, st)
	} else {
		rows, next, err = st.Backend.Read(ctx, cursor)
	}
	if err != nil {
		return nil, "", err
	}
	set, err := purgedSet(ctx, st)
	if err != nil {
		return nil, "", err
	}
	for i, sr := range rows {
		if rows[i], _, err = hidePurged(sr, set); err != nil {
			return nil, "", err
		}
		if rows[i], err = hideTrashed(rows[i]); err != nil { // DELETION 3.2
			return nil, "", err
		}
	}
	return rows, next, nil
}

// readTable is readRows from the start narrowed to one table before the
// purge and trash filters, which look at each row on its own, so the rows
// are the same as readRows' rows of that table. A read of a small table
// (policies, actors) then never searches every big row file's bytes for a
// trash mark (BUG-TIME-BENCH).
func (e *Engine) readTable(ctx context.Context, name store.Source, table row.Table) ([]store.Row, error) {
	st := e.stores[name]
	all, err := readOneTable(ctx, st, table)
	if err != nil {
		return nil, err
	}
	set, err := purgedSet(ctx, st)
	if err != nil {
		return nil, err
	}
	var rows []store.Row
	for _, sr := range all {
		if sr.Table != table {
			continue
		}
		if sr, _, err = hidePurged(sr, set); err != nil {
			return nil, err
		}
		if sr, err = hideTrashed(sr); err != nil { // DELETION 3.2
			return nil, err
		}
		rows = append(rows, sr)
	}
	return rows, nil
}

// Resurrected is one row file that holds a live row of an id its folder
// lists as purged: an old clone or a hand copy brought it back. Reads hide
// it (hidePurged); `lore doctor` names it so it can be removed, and
// `lore purge <id>` again writes the tombstone over it.
type Resurrected struct {
	Store store.Source
	Table row.Table
	ID    ids.AnyID
}

// Resurrected lists every resurrected row file of every attached store
// (precedence order, then table and id as the backend returns them).
func (e *Engine) Resurrected(ctx context.Context) ([]Resurrected, error) {
	var out []Resurrected
	seen := map[string]bool{} // one place attached twice is read once
	for _, name := range e.order {
		st := e.stores[name]
		if st.Location != "" {
			if seen[st.Location] {
				continue
			}
			seen[st.Location] = true
		}
		set, err := purgedSet(ctx, st)
		if err != nil {
			return nil, err
		}
		if len(set) == 0 {
			continue
		}
		rows, _, err := readAll(ctx, st)
		if err != nil {
			return nil, err
		}
		for _, sr := range rows {
			if !sr.Deleted && set[sr.ID.String()] {
				out = append(out, Resurrected{Store: name, Table: sr.Table, ID: sr.ID})
			}
		}
	}
	return out, nil
}
