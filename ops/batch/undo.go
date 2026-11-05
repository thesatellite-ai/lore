package batch

// `lore batch undo <bat_…>` (TSQ6, DELETION 2.8 / 3.13; Q-R3-1 was a
// coordinator default, built by FA-DEL-3):
//
//	undo ─► BatchEvents (this machine's log, Q-R3-8) ─► rows of the batch
//	     ├► any "update" / "purge" row ─► E_UNSUPPORTED (the log keeps no
//	     │   old values, so an edit cannot be reverted; FA-DEL-3)
//	     ├► "trash" rows (a delete batch) ─► Undoer.Restore(bat_) — the same
//	     │   write as `lore trash restore <bat_…>`; rows purged since are
//	     │   listed as skipped, never brought back (DQ4)
//	     └► "create" rows (an import, a bulk add) ─► ask (--yes) ─► one new
//	         delete batch moving each still-alive created row to the trash

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
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
	return op.Define(op.Spec{Name: OpUndo, Summary: "Undo a bulk action: restore a delete batch, or move the rows a create batch added to the trash",
		Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{MayAsk: true},
		Errors:   []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.NeedsConfirm, errs.Cancelled, errs.ParentDeleted, errs.Internal},
		Examples: []op.Example{{Title: "undo a bulk delete", CLI: "lore batch undo bat_<id>", Want: "restored"}}}, o.undo)
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
	var trashed, created []Event
	for _, ev := range mine {
		switch ev.Action {
		case actTrash:
			trashed = append(trashed, ev)
		case actCreate:
			created = append(created, ev)
		case actRestore, actDelete:
			// a restore already undone, or a tombstone row of the cascade: nothing to revert
		case actUpdate, actPurge:
			return UndoOut{}, errs.New(errs.Unsupported, "batch undo: "+id.String()+" "+ev.Action+"d "+ev.ID.String()+
				"; edits and purges cannot be undone (the batch log keeps no old values, and purged rows never come back, DQ4)").
				WithHint("see what it changed: lore batch show " + id.String() + " --all")
		default:
			return UndoOut{}, errs.New(errs.Unsupported, "batch undo: "+id.String()+" holds a "+strconv.Quote(ev.Action)+" row ("+ev.ID.String()+"); only delete and create batches can be undone")
		}
	}
	out := UndoOut{Batch: id.String(), Restored: []string{}, Trashed: []string{}, Skipped: []Skip{}}
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
	if err := bulk.Ask(ctx, "undoing "+id.String()+" moves the "+strconv.Itoa(len(evs))+" rows it created to the trash; continue?"); err != nil {
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
		b.WriteString("✓ restored " + strconv.Itoa(len(o.Restored)) + " rows of batch " + o.Batch + "\n")
	}
	if len(o.Trashed) > 0 {
		b.WriteString("✓ moved " + strconv.Itoa(len(o.Trashed)) + " rows batch " + o.Batch + " created to the trash (batch " + o.Into +
			"; restore: lore trash restore " + o.Into + ")\n")
	}
	if len(o.Restored) == 0 && len(o.Trashed) == 0 {
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
