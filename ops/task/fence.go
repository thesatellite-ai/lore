package task

// fence.go — fencing claim holders on checklist writes (TASK_SYSTEM_DESIGN
// 7.4: "writes by a claim holder are fenced like task note; writes by
// others are allowed", PASS3-A: the refusal is E_LOST_CLAIM).
//
//	checklist write ─► getChecklistForWrite ─► getChecklist (the row)
//	   ─► work.TeamFrom(ctx) ─► HolderFencer? ─► FenceHolder(id, claim_epoch)
//	      no team / no upgrade: allow (no claims to fence on this surface)
//	      the host says the caller held the task and lost it: E_LOST_CLAIM
//
// The check runs before the write, not in one atomic step with the signals
// ref (the core write path cannot reach that ref, as with FB-TASK-1's
// delete fence): a claim lost between the check and the write is caught
// by the holder's next fenced write (P3-TASK-FENCE-1).

import (
	"context"
	"strconv"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// HolderFencer is an optional upgrade of the host's Team: the fencing
// check for a write by a claim holder that is not a coordination verb (a
// checklist write, TASK 7.4). FenceHolder returns nil when the caller does
// not hold and never held taskID in this session (a reviewer may tick),
// or holds it with a live lease and rowEpoch equals the claim's epoch; it
// returns an E_LOST_CLAIM error when the caller's claim is gone, expired
// or fenced by a newer epoch. A host without it fences nothing.
type HolderFencer interface {
	FenceHolder(ctx context.Context, taskID string, rowEpoch int) error
}

// fenceHolder runs the host's HolderFencer on task row r, if any.
func fenceHolder(ctx context.Context, r row.Row) error {
	t, ok := work.TeamFrom(ctx)
	if !ok {
		return nil
	}
	f, ok := t.(HolderFencer)
	if !ok {
		return nil
	}
	return f.FenceHolder(ctx, r.ID.String(), rowClaimEpoch(r))
}

// rowClaimEpoch is the row's claim_epoch (0 when unset or unreadable, the
// value a never-fenced task has).
func rowClaimEpoch(r row.Row) int {
	v, ok := r.Fields[fieldClaimEpoch]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return 0
	}
	n, err := strconv.Atoi(v.String())
	if err != nil {
		return 0
	}
	return n
}

// getChecklistForWrite is getChecklist for a checklist write: the task row
// and its list, after the holder fence.
func (o ops) getChecklistForWrite(ctx context.Context, raw string) (clHit, error) {
	h, err := o.getChecklist(ctx, raw)
	if err != nil {
		return clHit{}, err
	}
	if err = fenceHolder(ctx, h.task); err != nil {
		return clHit{}, err
	}
	return h, nil
}
