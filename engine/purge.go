package engine

// Purge (DELETION_DESIGN 3.5, STORE_SERVER_SPEC 6.1): removing a row for
// good. The core's delete already writes a tombstone kept forever; a purge
// also lists the id in the folder's .lore/data/_purged.json, which
// v0.1.17 honours on ADOPT and git merges as a set union, and which the
// engine reads before every write: a purged id never holds a live row
// again (put refuses it, a natural-key create takes a fresh id).

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
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
// Delete does (relation rules, cascades; every tombstoned id is purged),
// a row already deleted (a tombstone) has only its id listed. Purging an
// id already listed is a no-op that still succeeds (run twice = once).
func (e *Engine) Purge(ctx context.Context, ref Ref) (Written, error) {
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	st, err := e.writable(src)
	if err != nil {
		return Written{}, err
	}
	p, ok := st.Backend.(Purger)
	if !ok {
		return Written{}, errs.New(errs.Unsupported, "engine: store "+string(src)+" cannot record purged ids")
	}
	if !cur.Deleted {
		return e.remove(ctx, src, cur, true)
	}
	if err := p.WritePurge(ctx, nil, []ids.AnyID{cur.ID}); err != nil {
		return Written{}, err
	}
	cl := e.callOf(ctx)
	ev := Event{Op: cl.op, Action: ActionPurge, Store: src, Table: cur.Table, ID: cur.ID, HLC: cur.HLC, Actor: e.actorOf(cl)}
	return Written{Row: cur}, e.finish(ctx, src, nil, ev)
}

// errPurgedID is the cause of refusePurged's error, so create can tell a
// purged natural id (take a fresh id) from any other refusal.
var errPurgedID = errors.New("engine: purged id")

// refusePurged fails when id is listed as purged in st (E_INVALID_ID: a
// purged id is never used again, DELETION_DESIGN DQ4). A tombstone write
// is allowed (it keeps the row gone). Backends without the list pass.
func refusePurged(ctx context.Context, st Attached, id ids.AnyID, deleted bool) error {
	p, ok := st.Backend.(Purger)
	if !ok || deleted {
		return nil
	}
	set, err := p.Purged(ctx)
	if err != nil {
		return err
	}
	if set[id.String()] {
		return errs.Wrap(errs.InvalidID, "engine: "+id.String()+" was purged from store "+string(st.Name)+"; a purged id never comes back", errPurgedID).
			WithHint("write the content as a new row (a new id)")
	}
	return nil
}
