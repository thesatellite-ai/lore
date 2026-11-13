package batch

// `lore batch undo <bat_…>` (TSQ6, DELETION 2.8 / 3.13; Q-R3-1 was a
// coordinator default, built by FA-DEL-3):
//
//	undo ─► BatchEvents (this machine's log, Q-R3-8) ─► rows of the batch
//	     ├► any "purge" row, or an "update" row without a before-image
//	     │   (a batch older than R14's log) ─► E_UNSUPPORTED
//	     ├► "update" rows (an edit batch: meta set/unset, resource edit or
//	     │   check, the edits of an import; R14, PASS3-A-6) ─► every row
//	     │   still at the batch's hlc? else E_STALE naming the rows changed
//	     │   since, nothing written (P3-BATCH-1) ─► one new batch of the
//	     │   same kind putting each row back to its image before the batch
//	     ├► "trash" rows (a delete batch) ─► Undoer.Restore(bat_) — the same
//	     │   write as `lore trash restore <bat_…>`; rows purged since are
//	     │   listed as skipped, never brought back (DQ4)
//	     └► "create" rows (an import, a bulk add) ─► ask (--yes) ─► one new
//	         delete batch moving each still-alive created row to the trash

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// OpUndo is `lore batch undo`.
const OpUndo = "batch.undo"

// Batch log actions undo reads (engine.Action values, as the log keeps them).
const (
	actCreate  = string(engine.ActionCreate)
	actUpdate  = string(engine.ActionUpdate)
	actPurge   = string(engine.ActionPurge)
	actDelete  = string(engine.ActionDelete)
	actTrash   = string(engine.ActionTrash)
	actRestore = string(engine.ActionRestore)
)

// Undoer is the optional Env upgrade undo writes through (*wire.App has it).
type Undoer interface {
	Restore(ctx context.Context, target string) (engine.TrashResult, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
}

// Reverter is the optional Env upgrade an edit batch's undo needs (*wire.App
// has it): the before-images the batch log kept (R14) and the write that
// puts a row back. Without it an edit batch stays E_UNSUPPORTED.
type Reverter interface {
	BatchPriors(ctx context.Context, id ids.ID[ids.Batch]) (map[ids.AnyID]Prior, error)
	Revert(ctx context.Context, ref engine.Ref, want hlc.HLC, before row.Row) (engine.Written, error)
}

// Prior is one row an edit batch changed: the row before the batch's first
// write of it, and the hlc its last write left.
type Prior struct {
	Store  store.Source
	Before row.Row
	After  hlc.HLC
}

// UndoIn is the input of batch.undo.
type UndoIn struct {
	ID string `json:"id" arg:"0" desc:"the batch id (bat_…)"`
}

// UndoOut is the output of batch.undo.
type UndoOut struct {
	Batch string `json:"batch"`
	// Restored lists rows a delete batch's undo brought back.
	Restored []string `json:"restored"`
	// Trashed lists rows a create batch's undo moved to the trash, under
	// the new delete batch Into.
	Trashed []string `json:"trashed"`
	Into    string   `json:"into,omitempty"`
	// Reverted lists rows an edit batch's undo put back to their values
	// before the batch, under the new batch Reverts (itself undoable).
	Reverted []string `json:"reverted"`
	Reverts  string   `json:"reverts,omitempty"`
	// Skipped names rows left alone and why ("purged", "already gone").
	Skipped []Skip `json:"skipped"`
}

// Skip is one row undo left alone.
type Skip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// undoEntry is the batch.undo operation.
func undoEntry(o ops) op.Entry {
	return op.Define(op.Spec{Name: OpUndo, Summary: "Undo a bulk action: restore a delete batch, put the rows an edit batch changed back, or move the rows a create batch added to the trash",
		Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{MayAsk: true},
		Errors:   []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Stale, errs.NeedsConfirm, errs.Cancelled, errs.ParentDeleted, errs.Internal},
		Examples: []op.Example{{Title: "undo a bulk delete", CLI: "lore batch undo --id bat_<id>", Want: "restored"}}}, o.undo)
}

func (o ops) undo(ctx context.Context, in UndoIn) (UndoOut, error) {
	id, err := ids.Parse[ids.Batch](strings.TrimSpace(in.ID))
	if err != nil {
		return UndoOut{}, lookup.BadID("batch undo: \""+textcut.Cut(strings.TrimSpace(in.ID), maxQuotedID, maxQuotedID)+"\" is not a batch id (bat_…)", err)
	}
	u, ok := upgrade.As[Undoer](o.env)
	if !ok {
		return UndoOut{}, errs.New(errs.Unsupported, "batch undo: this surface cannot write")
	}
	evs, err := o.env.BatchEvents(ctx)
	if err != nil {
		return UndoOut{}, err
	}
	var mine []Event
	for _, ev := range evs {
		if ev.Batch == id {
			mine = append(mine, ev)
		}
	}
	if len(mine) == 0 {
		return UndoOut{}, errs.New(errs.NotFound, "batch "+id.String()+" is not in this machine's batch log").
			WithHint("lore batch list shows the batches run on this machine")
	}
	var trashed, created, updated []Event
	for _, ev := range mine {
		switch ev.Action {
		case actTrash:
			trashed = append(trashed, ev)
		case actCreate:
			created = append(created, ev)
		case actRestore, actDelete:
			// a restore already undone, or a tombstone row of the cascade: nothing to revert
		case actUpdate:
			updated = append(updated, ev)
		case actPurge:
			return UndoOut{}, errs.New(errs.Unsupported, "batch undo: "+id.String()+" purged "+ev.ID.String()+
				"; purged rows never come back (DQ4)").
				WithHint("see what it changed: lore batch show --id " + id.String() + " --all")
		default:
			return UndoOut{}, errs.New(errs.Unsupported, "batch undo: "+id.String()+" holds a "+strconv.Quote(ev.Action)+" row ("+ev.ID.String()+"); only delete and create batches can be undone")
		}
	}
	out := UndoOut{Batch: id.String(), Restored: []string{}, Trashed: []string{}, Reverted: []string{}, Skipped: []Skip{}}
	if len(updated) > 0 {
		// edits first: an import's edited row may point at a row it created
		if err = o.undoEdits(ctx, id, updated, &out); err != nil {
			return out, err
		}
	}
	if len(trashed) > 0 {
		if err = o.undoDelete(ctx, u, id, trashed, &out); err != nil {
			return out, err
		}
	}
	if len(created) > 0 {
		if err = o.undoCreate(ctx, u, id, created, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// undoEdits puts every row an edit batch changed back to its image before
// the batch (R14, PASS3-A-6). It checks every row first and refuses with
// nothing written when one changed (or was deleted) after the batch
// (P3-BATCH-1: undo never overwrites a later edit); then it writes one new
// batch of the same kind, so the undo is itself undoable.
func (o ops) undoEdits(ctx context.Context, id ids.ID[ids.Batch], evs []Event, out *UndoOut) error {
	rv, ok := upgrade.As[Reverter](o.env)
	if !ok {
		return errs.New(errs.Unsupported, "batch undo: this surface cannot put edited rows back")
	}
	priors, err := rv.BatchPriors(ctx, id)
	if err != nil {
		return err
	}
	order, err := editOrder(id, evs, priors)
	if err != nil {
		return err
	}
	back, err := o.refuseChanged(ctx, id, order, priors)
	if err != nil {
		return err
	}
	todo := make([]ids.AnyID, 0, len(order))
	for _, rid := range order {
		if back[rid] {
			out.Skipped = append(out.Skipped, Skip{ID: rid.String(), Reason: reasonAlreadyBack})
			continue
		}
		todo = append(todo, rid)
	}
	if len(todo) == 0 {
		return nil // undone already (or set back by hand): nothing to write, no new batch
	}
	order = todo
	bctx, into, err := bulk.Begin(ctx, revertKind(evs[0]))
	if err != nil {
		return err
	}
	for i, rid := range order {
		p := priors[rid]
		if _, werr := rv.Revert(bctx, engine.Ref{Store: p.Store, Table: p.Before.Table, ID: rid}, p.After, p.Before); werr != nil {
			return errs.Prefix("batch undo: stopped after "+strconv.Itoa(i)+" of "+strconv.Itoa(len(order))+
				" rows (the rows put back are in batch "+into.String()+"; undo: lore batch undo --id "+into.String()+")", werr)
		}
		out.Reverted = append(out.Reverted, rid.String())
	}
	out.Reverts = into.String()
	return nil
}

// editOrder is each edited row once, in the order the batch first wrote
// it. An update with no before-image (a batch written before the log kept
// them, or by a write path that keeps none) is E_UNSUPPORTED.
func editOrder(id ids.ID[ids.Batch], evs []Event, priors map[ids.AnyID]Prior) ([]ids.AnyID, error) {
	seen := map[ids.AnyID]bool{}
	var order []ids.AnyID
	for _, ev := range evs {
		if seen[ev.ID] {
			continue
		}
		seen[ev.ID] = true
		if _, ok := priors[ev.ID]; !ok {
			return nil, errs.New(errs.Unsupported, "batch undo: "+id.String()+" updated "+ev.ID.String()+
				" but the batch log kept no earlier values for it, so the edit cannot be reverted").
				WithHint("see what it changed: lore batch show --id " + id.String() + " --all")
		}
		order = append(order, ev.ID)
	}
	return order, nil
}

// reasonAlreadyBack is the Skip reason of an edited row that already holds
// its values from before the batch (the batch was undone already, or the
// values were set back by hand): putting it back would change nothing.
const reasonAlreadyBack = "already at its values before the batch"

// refuseChanged is E_STALE naming every row that changed (or was deleted)
// after the batch, before anything is written. A row whose values are the
// ones it had before the batch is not a later edit undo would overwrite: it
// is returned in back (the batch was undone already, or set back by hand),
// so a second undo says "nothing to undo" like a delete batch does.
func (o ops) refuseChanged(ctx context.Context, id ids.ID[ids.Batch], order []ids.AnyID, priors map[ids.AnyID]Prior) (map[ids.AnyID]bool, error) {
	back := map[ids.AnyID]bool{}
	g, ok := upgrade.As[getter](o.env)
	if !ok {
		return back, nil // Revert checks each row again under its Expect
	}
	var changed []string
	for _, rid := range order {
		p := priors[rid]
		_, cur, err := g.Get(ctx, engine.Ref{Store: p.Store, Table: p.Before.Table, ID: rid})
		switch {
		case errs.Has(err, errs.NotFound):
			changed = append(changed, rid.String()+" (gone)")
		case err != nil:
			return nil, err
		case cur.Deleted:
			changed = append(changed, rid.String()+" (deleted)")
		case cur.HLC.Compare(p.After) == 0:
			// untouched since the batch: undo puts it back
		case sameValues(cur, p.Before):
			back[rid] = true
		default:
			changed = append(changed, rid.String())
		}
	}
	if len(changed) == 0 {
		return back, nil
	}
	return nil, errs.New(errs.Stale, "batch undo: "+id.String()+" not undone: "+strconv.Itoa(len(changed))+" of its rows changed after the batch: "+
		strings.Join(changed, ", ")+"; nothing written (undo never overwrites a later edit)").
		WithHint("lore batch list shows later bulk edits; undo those first, or set the values back by hand")
}

// sameValues reports whether cur holds exactly the content of before: the
// same canonical document once the write stamps (_hlc, updated_at,
// updated_by) and the on-disk format version are set aside, since every
// write, a revert included, moves those. A row that cannot be encoded is
// never "the same" (the caller then treats it as changed: E_STALE).
func sameValues(cur, before row.Row) bool {
	before.HLC, before.UpdatedAt, before.UpdatedBy, before.SourceVersion = cur.HLC, cur.UpdatedAt, cur.UpdatedBy, cur.SourceVersion
	a, aerr := row.Encode(cur)
	b, berr := row.Encode(before)
	return aerr == nil && berr == nil && bytes.Equal(a, b)
}

// getter is the read refuseChanged uses (*wire.App has it).
type getter interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// revertKind is the kind of the batch an undo of edits writes: the kind
// of the batch undone, except that an import's edits are put back as an
// edit of their noun (the undo imports nothing).
func revertKind(ev Event) bulk.Kind {
	if ev.Kind != bulk.KindImport {
		return ev.Kind
	}
	switch {
	case strings.HasPrefix(ev.Op, "meta."):
		return bulk.KindMeta
	case strings.HasPrefix(ev.Op, "task."):
		return bulk.KindTask
	default:
		return bulk.KindResource
	}
}

// undoDelete restores a delete batch: the same write as `lore trash
// restore <bat_…>` (DELETION 2.8). Rows of the batch no longer in the
// trash (purged since, or restored already) are reported as skipped.
func (o ops) undoDelete(ctx context.Context, u Undoer, id ids.ID[ids.Batch], evs []Event, out *UndoOut) error {
	res, err := u.Restore(ctx, id.String())
	if err != nil && !errs.Has(err, errs.NotFound) {
		return err
	}
	back := map[ids.AnyID]bool{}
	for _, t := range res.Rows {
		back[t.Row.ID] = true
		out.Restored = append(out.Restored, t.Row.ID.String())
	}
	for _, ev := range evs {
		if !back[ev.ID] {
			out.Skipped = append(out.Skipped, Skip{ID: ev.ID.String(), Reason: "not in the trash (purged or restored since)"})
		}
	}
	return nil
}

// undoCreate moves every row a create batch added and that is still alive
// to the trash, as one new delete batch, after asking (--yes): an undo of
// an import is a delete, and a delete is restorable (DQ8).
func (o ops) undoCreate(ctx context.Context, u Undoer, id ids.ID[ids.Batch], evs []Event, out *UndoOut) error {
	if err := bulk.Ask(ctx, "undoing "+id.String()+" moves the "+plural.Count(len(evs), "row")+" it created to the trash; continue?"); err != nil {
		return err
	}
	dctx, into, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return err
	}
	dctx = engine.WithDeleteReason(dctx, "undo of "+id.String())
	for _, ev := range evs {
		_, derr := u.Delete(dctx, engine.Ref{Store: ev.Store, Table: ev.Table, ID: ev.ID})
		switch {
		case derr == nil:
			out.Trashed = append(out.Trashed, ev.ID.String())
		case errs.Has(derr, errs.InTrash), errs.Has(derr, errs.NotFound):
			out.Skipped = append(out.Skipped, Skip{ID: ev.ID.String(), Reason: "already deleted"})
		default:
			return derr
		}
	}
	if len(out.Trashed) > 0 {
		out.Into = into.String()
	}
	return nil
}

// Text implements op.Texter.
func (o UndoOut) Text() string {
	var b strings.Builder
	if len(o.Restored) > 0 {
		b.WriteString("✓ restored " + plural.Count(len(o.Restored), "row") + " of batch " + o.Batch + "\n")
	}
	if len(o.Trashed) > 0 {
		b.WriteString("✓ moved " + plural.Count(len(o.Trashed), "row") + " batch " + o.Batch + " created to the trash (batch " + o.Into +
			"; restore: lore trash restore --id " + o.Into + ")\n")
	}
	if len(o.Reverted) > 0 {
		b.WriteString("✓ restored " + plural.Count(len(o.Reverted), "row") + " to their values before " + o.Batch +
			"; batch " + o.Reverts + " (undo: lore batch undo --id " + o.Reverts + ")\n")
	}
	if len(o.Restored) == 0 && len(o.Trashed) == 0 && len(o.Reverted) == 0 {
		b.WriteString("nothing to undo in batch " + o.Batch + "\n")
	}
	if len(o.Skipped) > 0 {
		b.WriteString("  skipped " + strconv.Itoa(len(o.Skipped)) + ":\n")
		for _, s := range o.Skipped {
			b.WriteString("    " + s.ID + " " + s.Reason + "\n")
		}
	}
	return b.String()
}
