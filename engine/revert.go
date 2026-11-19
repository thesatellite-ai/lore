package engine

import (
	"context"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// WarnUndoUncovered warns that an edit inside a batch is stored, but its
// before-image could not be kept, so `lore batch undo` cannot put that row
// back (R14).
const WarnUndoUncovered WarnCode = "W_UNDO_UNCOVERED"

// Revert puts a row back to an earlier image: the write behind `lore
// batch undo` of an edit batch (R14, PASS3-A-6). before is the row as it
// was just before the batch's first write of it (fields and metadata, as
// the batch log kept them); want is the hlc the batch's last write of the
// row left. Flow:
//
//	locate ─► deleted, or hlc ≠ want ─► E_STALE (the row changed after the
//	          batch: undo never overwrites a later edit, P3-BATCH-1)
//	  ─► fields = before's fields (retyped), meta = before's meta
//	  ─► whole-row validation + changed refs (a ref may point at a row
//	     deleted since) ─► put with Expect = the hlc read ─► cache + audit
//	     (an update event, so the revert is itself one batch's edit and can
//	     be undone the same way)
//
// The values are not scanned for secrets again: they are the values the
// row held, already through the write checks when first stored (P3-BATCH-2).
func (e *Engine) Revert(ctx context.Context, ref Ref, want hlc.HLC, before row.Row) (Written, error) {
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, errs.New(errs.Stale, cur.ID.String()+" was deleted after the batch")
	}
	if cur.HLC.Compare(want) != 0 {
		return Written{}, errs.New(errs.Stale, cur.ID.String()+" changed after the batch")
	}
	st, err := e.writable(ctx, src)
	if err != nil {
		return Written{}, err
	}
	def, err := e.tableDef(cur.Table)
	if err != nil {
		return Written{}, err
	}
	whole, err := validate.Fields(def, retype(def, before.Fields), validate.Whole)
	if err != nil {
		return Written{}, err
	}
	was := retype(def, cur.Fields)
	var changed []string
	for k, v := range whole {
		if old, ok := was[k]; !ok || !value.Equal(old, v) {
			changed = append(changed, k)
		}
	}
	slices.Sort(changed)
	if err = e.checkRefs(ctx, src, def, whole, changed); err != nil {
		return Written{}, err
	}
	ours := cur
	ours.Fields = whole
	ours.Meta = maps.Clone(before.Meta)
	cl := e.callOf(ctx)
	actor := e.actorOf(cl)
	ours.HLC = e.clock.Now(cur.HLC)
	ours.UpdatedAt, ours.UpdatedBy = ours.HLC.Wall, actor
	got, err := e.put(ctx, st, ours, cur.HLC)
	if err != nil {
		return Written{}, err
	}
	ev := Event{Op: cl.op, Action: ActionUpdate, Store: src, Table: cur.Table, ID: cur.ID, HLC: got.row.HLC, Actor: actor}
	aw, err := e.finishW(ctx, src, got.docs(), ev)
	return Written{Row: got.row, Warnings: aw}, err
}
