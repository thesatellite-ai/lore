package coord

// task.reassign (TASK 17.2, 18.1, TSQ6, XQ1, XQ2):
//
//	reassign ─► an id ─► Host.TaskReassign (one task, no batch, as v0.1.17)
//	         └► --from ─► Host.TaskReassignPlan (open tasks of that person; nothing written)
//	              ─► none: "no open tasks assigned to …"
//	              ─► bulk.Preview (E_NEEDS_CONFIRM above batch.preview_rows)
//	              ─► bulk.Begin(task) ─► Host.TaskReassign per id ─► one batch log event per task
//	              ─► "✓ N tasks reassigned to @x (batch bat_…)"; a failure names the batch
//
// The rows are written by the host's signals path (each reassign bumps
// claim_epoch with the board claim it drops, atomically), not by the
// engine, so the engine cannot stamp the batch on their audit events: the
// op records each event itself through the optional BatchRecorder
// (P3-COORD-2). The batch log keeps no before-image of these rows, so
// `lore batch undo` of one answers E_UNSUPPORTED naming `lore batch show`.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
)

// BatchRecorder is the optional Env upgrade that appends one event to the
// machine's batch log (wire.App has it): the batch writes of task reassign
// --from go through the host, not the engine, so nothing else records them.
// Without it the tasks are still reassigned under one bat_ id (named in the
// output and the error of a partial run), but `lore batch list|show` do not
// see the batch.
type BatchRecorder interface {
	RecordBatchEvent(ctx context.Context, ev engine.Event) error
}

// Output words of task reassign --from.
const (
	reassignWhat   = "task reassign --from"
	reassignAction = engine.ActionUpdate
	successMark    = "✓"
	noBatchLogNote = "  (this surface keeps no batch log: lore batch show cannot list it)"
)

func (o ops) reassign(ctx context.Context, in ReassignIn) (ReassignOut, error) {
	h, err := o.host(ctx, OpTaskReassign)
	if err != nil {
		return ReassignOut{}, err
	}
	if strings.TrimSpace(in.ID) != "" || strings.TrimSpace(in.From) == "" {
		// one task, or neither (the host answers the usage error, as v0.1.17)
		r, rerr := h.TaskReassign(ctx, in)
		out := ReassignOut{Lines: r.Lines, Tasks: []string{}}
		if rerr == nil && strings.TrimSpace(in.ID) != "" {
			out.Tasks = []string{strings.TrimSpace(in.ID)}
		}
		return out, rerr
	}
	plan, err := h.TaskReassignPlan(ctx, in)
	if err != nil {
		return ReassignOut{}, err
	}
	if len(plan.Tasks) == 0 {
		return ReassignOut{Lines: []string{"no open tasks assigned to " + in.From}, Tasks: []string{}}, nil
	}
	items := make([]bulk.Item, len(plan.Tasks))
	for i, t := range plan.Tasks {
		items[i] = bulk.Item{ID: t.ID, Label: t.Title}
	}
	if err = bulk.Preview(ctx, o.env, plan.Store, reassignWhat, items); err != nil {
		return ReassignOut{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return ReassignOut{}, err
	}
	b, _ := engine.BatchOf(bctx) // Begin just set it
	rec, logged := upgrade.As[BatchRecorder](o.env)
	actor, _ := ids.Parse[ids.Actor](plan.Actor) // zero when the host's caller has no v2 actor id: the event omits it
	out := ReassignOut{Batch: batch.String(), Tasks: []string{}}
	for _, t := range plan.Tasks {
		r, rerr := h.TaskReassign(bctx, ReassignIn{ID: t.ID, Assign: in.Assign})
		out.Lines = append(out.Lines, r.Lines...)
		if rerr != nil {
			// Partial failure names the batch, so the tasks already changed
			// can be found as one batch.
			return out, errs.Prefix(reassignWhat+": "+t.ID+" failed after "+strconv.Itoa(len(out.Tasks))+
				" earlier tasks of batch "+batch.String()+" changed (lore batch show "+batch.String()+")", rerr)
		}
		out.Tasks = append(out.Tasks, t.ID)
		if logged {
			if err = recordReassign(bctx, rec, b, plan, actor, t.ID); err != nil {
				return out, err
			}
		}
	}
	out.Lines = append(out.Lines, successMark+" "+strconv.Itoa(len(out.Tasks))+" tasks reassigned to "+in.Assign+
		" (batch "+batch.String()+"); lore batch show "+batch.String())
	if !logged {
		out.Lines = append(out.Lines, noBatchLogNote)
	}
	return out, nil
}

// recordReassign appends the batch log event of one reassigned task: names
// and ids only (engine.Event's rule), the wall time of now.
func recordReassign(ctx context.Context, rec BatchRecorder, b engine.Batch, plan ReassignPlan, actor ids.ID[ids.Actor], taskID string) error {
	id, err := ids.ParseAny(taskID)
	if err != nil {
		return errs.Wrap(errs.Internal, reassignWhat+": the host named "+taskID+", not a task id", err)
	}
	return rec.RecordBatchEvent(ctx, engine.Event{Op: OpTaskReassign, Action: reassignAction, Store: plan.Store,
		Table: schema.TableTasks, ID: id, Actor: actor, HLC: hlc.HLC{Wall: time.Now().UTC()}, Batch: b})
}
