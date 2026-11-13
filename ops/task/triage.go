package task

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// Triage actions (TASK 18.3): no prompts, so agents can drive them.
//
//	accept ─► edit (commitment accepted + optional mission/priority)
//	decline ─► cancel (close_reason from --reason, wont_do default)
//	        └─ --note ─► a comment on the task (notes path of task note)

// Operation names.
const (
	OpAccept  = "task.accept"
	OpDecline = "task.decline"
)

// declineDefault is decline's close_reason when --reason is left out (18.3).
const declineDefault = "wont_do"

// AcceptIn is the input of task.accept.
type AcceptIn struct {
	ID       string `json:"id" arg:"0" desc:"the proposed task (tsk_…)"`
	Mission  string `json:"mission,omitempty" flag:"mission" desc:"attach to mission_id (msn_*)"`
	Priority string `json:"priority,omitempty" flag:"priority" desc:"low | medium | high | urgent"`
}

// DeclineIn is the input of task.decline.
type DeclineIn struct {
	ID     string `json:"id" arg:"0" desc:"the proposed task (tsk_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"close reason: wont_do (default) | duplicate | obsolete | stale | cancelled"`
	Note   string `json:"note,omitempty" flag:"note" desc:"why, kept as a comment on the task"`
}

func triageOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAccept, Summary: "Accept a proposed task (commitment accepted)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
			Examples: []op.Example{{Title: "accept into a mission", CLI: "lore task accept --id tsk_<id> --mission msn_<id> --priority high", Want: "tsk_"}},
		}, o.accept),
		op.Define(op.Spec{
			Name: OpDecline, Summary: "Decline a proposed task (cancelled, with a close reason)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.OpenChildren, errs.Refused),
			Examples: []op.Example{{Title: "decline with a note", CLI: `lore task decline --id tsk_<id> --reason wont_do --note "we keep the name"`, Want: "cancelled"}},
		}, o.decline),
	}
}

func (o ops) accept(ctx context.Context, in AcceptIn) (work.WriteOut, error) {
	return o.edit(ctx, EditIn{ID: in.ID, Commitment: schema.TaskCommitmentAccepted, Mission: in.Mission, Priority: in.Priority})
}

func (o ops) decline(ctx context.Context, in DeclineIn) (DoneOut, error) {
	// decline answers a proposal (docs/tasks/commitment-and-snooze.md
	// Step 3); on an accepted or someday task it is refused, so a "no" to
	// a proposal can never cancel work someone took on (INTEG-QUESTIONS-6).
	id, err := taskID(in.ID)
	if err != nil {
		return DoneOut{}, err
	}
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return DoneOut{}, err
	}
	if c := cur.Fields[fieldCommitment].String(); c != schema.TaskCommitmentProposed {
		if c == "" {
			c = schema.TaskCommitmentAccepted // the field's default
		}
		return DoneOut{}, errs.New(errs.Refused, "task decline: "+id.String()+" is "+c+", not proposed; decline only answers a proposal").
			WithHint("to stop it, lore task cancel --id " + id.String() + " --reason obsolete")
	}
	reason := in.Reason
	if reason == "" {
		reason = declineDefault
	}
	out, err := o.cancel(ctx, CancelIn{ID: in.ID, Reason: reason})
	if err != nil || in.Note == "" {
		return out, err
	}
	if err = o.addNote(ctx, in.ID, in.Note); err != nil {
		return out, err
	}
	out.Human += "  note kept as a comment\n"
	return out, nil
}

// addNote writes a comment row on task id in the task's store.
func (o ops) addNote(ctx context.Context, rawID, body string) error {
	id, err := taskID(rawID)
	if err != nil {
		return err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return err
	}
	fields := map[string]value.Value{}
	if fields[schema.FieldEntityTable], err = value.TextOf(string(table)); err != nil {
		return err
	}
	if fields[schema.FieldEntityID], err = value.RefOf(r.ID); err != nil {
		return err
	}
	if fields["body"], err = value.TextOf(body); err != nil {
		return err
	}
	if a := o.currentActorRef(ctx); a != nil && a.Kind() != value.KindNull {
		fields["created_by_actor_id"] = a
	}
	_, err = o.env.Create(ctx, src, row.Table(schema.TableComments), fields)
	return err
}
