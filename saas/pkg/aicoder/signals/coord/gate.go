package coord

import (
	"context"
	"fmt"
	"time"

	"dbent/schema"
	"saas/pkg/aicoder/signals"
)

// Gate states (dbent/schema TaskGate).
const (
	gateRequired  = string(schema.TaskGateRequired)
	gateRequested = string(schema.TaskGateRequested)
	gateApproved  = string(schema.TaskGateApproved)
)

// ApproveRequest is one `lore gate approve` (§7.4 "Gates", TEAM-028).
//
// The caller has already established that the approver is a human from the
// identity file or config (never LORE_ACTOR) and, when the task's mission
// names approvers, one of them: those checks need the identity and the
// mission, which live outside this package.
type ApproveRequest struct {
	ID string
	// ApproverID is the approving person's actor id.
	ApproverID string
}

// Approval is a recorded approval.
type Approval struct {
	Hash string
	At   time.Time
}

// ErrNotGated means the task has no gate to approve.
var ErrNotGated = fmt.Errorf("coord: task has no gate")

// Approve records an approval atomically with a lease on the signals ref
// (so it is confirmed or fails closed, rule 11): gate approved, approved_by,
// approved_at and approved_hash over title + body, plus a claim_log entry.
func Approve(ctx context.Context, env Env, req ApproveRequest) (Approval, error) {
	var out Approval
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		doc, ok, err := env.Rows.Get(ctx, req.ID)
		if err != nil {
			return signals.Change{}, err
		}
		if !ok {
			return signals.Change{}, fmt.Errorf("%w: %s", ErrUnknownTask, req.ID)
		}
		if doc.Str(keyGate) == "" {
			return signals.Change{}, fmt.Errorf("%w: %s (request one with `lore gate request %s`)", ErrNotGated, req.ID, req.ID)
		}
		next := doc.Clone()
		at := v.Now.UTC().Truncate(time.Second)
		out = Approval{Hash: GateHash(doc.Str(keyTitle), doc.Str(keyBody)), At: at}
		next[keyGate] = gateApproved
		next.SetStr(keyApprovedBy, req.ApproverID)
		next.SetTime(keyApprovedAt, at)
		next.SetStr(keyApprovedSum, out.Hash)
		if err := next.AppendClaimLog(schema.ClaimLogEntry{At: at, Event: schema.ClaimEventApprove, ActorID: req.ApproverID, Epoch: doc.Epoch()}); err != nil {
			return signals.Change{}, err
		}
		next.touch(v.Now)
		comps, err := env.Rows.Stage(ctx, RowWrite{ID: req.ID, Doc: next})
		if err != nil {
			return signals.Change{}, err
		}
		return signals.Change{Companions: comps}, nil
	})
	return out, err
}

// RequestGate is `lore gate request` by the holder of the task (fenced, F7
// table) — the mutation for HolderWrite — or by anyone on an unclaimed
// task. An approved gate whose hash still matches is left alone.
func RequestGate(d Doc, _ time.Time) ([]RowWrite, error) {
	if d.Str(keyGate) == gateApproved && !Gated(d) {
		return nil, nil
	}
	d[keyGate] = gateRequested
	d[keyApprovedBy], d[keyApprovedAt], d[keyApprovedSum] = nil, nil, nil
	return nil, nil
}

// GateState describes a row's gate for views.
func GateState(d Doc) string {
	switch g := d.Str(keyGate); {
	case g == "":
		return ""
	case g == gateApproved && !Gated(d):
		return "approved"
	case g == gateApproved:
		return "approval voided by an edit"
	case g == gateRequired:
		return "needs approval"
	default:
		return "approval requested"
	}
}
