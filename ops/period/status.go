package period

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// ActorStatusIn is the input of actor.status.
type ActorStatusIn struct {
	Actor string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	To    string `json:"to" arg:"1" desc:"active, on_leave, left or archived"`
	From  string `json:"from,omitempty" flag:"from" desc:"with on_leave: first day of the leave, YYYY-MM-DD (default today)"`
	Until string `json:"until,omitempty" flag:"until" desc:"with on_leave: last day of the leave, YYYY-MM-DD (optional; never flips the status on its own)"`
	Note  string `json:"note,omitempty" flag:"note" desc:"why (\"parental leave\"); kept on the person until they are active again (not with active)"`
}

// ActorStatusOut is the status change.
type ActorStatusOut struct {
	ID         string `json:"id"`
	Name       string `json:"display_name"`
	From       string `json:"from"`
	To         string `json:"to"`
	LeaveFrom  string `json:"leave_from,omitempty"`
	LeaveUntil string `json:"leave_until,omitempty"`
	// Note is the status note the person now has (--note, or the one
	// kept from before; "" when none, always "" for active).
	Note string `json:"note,omitempty"`
	// Changed is false for a no-op (the status was already to).
	Changed bool   `json:"changed"`
	Where   string `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Text is "✓ act_… Mira Patel (@mira): active → on_leave (2026-06-01 → 2026-06-30)".
func (o ActorStatusOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o ActorStatusOut) text() string {
	if !o.Changed {
		return success + " " + o.ID + " " + o.Name + " is already " + o.To + " (nothing written)\n" + noteLine(o.Note)
	}
	if o.From == o.To && o.To != schema.ActorStatusOnLeave {
		// A note-only write (left --note …).
		return success + " " + o.ID + " " + o.Name + " is already " + o.To + "; note updated\n" + noteLine(o.Note)
	}
	s := success + " " + o.ID + " " + o.Name + ": " + o.From + arrow + o.To
	if o.To == schema.ActorStatusOnLeave {
		end := o.LeaveUntil
		if end == "" {
			end = "open-ended"
		}
		s += " (" + o.LeaveFrom + arrow + end + ")"
	}
	return s + "\n" + noteLine(o.Note)
}

// noteLine is "  note: parental leave\n", "" without a note.
func noteLine(note string) string {
	if note == "" {
		return ""
	}
	return "  note: " + note + "\n"
}

// move is what a status change does.
type move int

const (
	moveNoop  move = iota // already there: nothing written
	moveWrite             // write the actor row
)

// statusMove applies the TEAM_PROFILES 5.3 table: from a person's status
// to another. It refuses every change that belongs to offboard or rejoin
// (they also end or start a stint and memberships), with that command as
// the hint. Non-human actors have no stint: an owner may move an agent
// between active, left and archived freely (5.3: "an owner can reactivate
// one after a rejoin"), and offboard/rejoin refuse them.
func statusMove(human bool, from, to string) (move, error) {
	if !validStatus(to) {
		return moveNoop, errs.New(errs.InvalidInput, "status "+quote(to)+" is not one of "+strings.Join(schema.ActorStatuses(), listSep))
	}
	if from == to && to != schema.ActorStatusOnLeave {
		return moveNoop, nil
	}
	if !human {
		return moveWrite, nil
	}
	gone := func(s string) bool { return s == schema.ActorStatusLeft || s == schema.ActorStatusArchived }
	switch {
	case gone(from) && !gone(to):
		return moveNoop, errs.New(errs.StatusChange, "they left: going "+to+" starts a new stint").
			WithHint("lore actor rejoin <actor> --type employee|contractor|intern|client_contact")
	case !gone(from) && to == schema.ActorStatusLeft:
		return moveNoop, errs.New(errs.StatusChange, "leaving ends their stint and project memberships").
			WithHint("lore actor offboard <actor>")
	case !gone(from) && to == schema.ActorStatusArchived:
		return moveNoop, errs.New(errs.StatusChange, "archiving someone still here ends their stint and project memberships").
			WithHint("lore actor offboard <actor> --archive")
	}
	return moveWrite, nil
}

// quote quotes s for a message ("" shows as "").
func quote(s string) string { return `"` + s + `"` }

// validStatus reports whether s is a status.
func validStatus(s string) bool {
	for _, x := range schema.ActorStatuses() {
		if s == x {
			return true
		}
	}
	return false
}

// statusPatch is the actor patch that sets status to, keeping the v0.1.17
// active/left_at in step (TEAM_PROFILES 2.1, 2.7: v0.1.17 readers and
// rosterFind read active): active and on_leave are active true with no
// left_at; left and archived are active false with left_at (on, unless the
// row already has one: archiving someone who left keeps their left day).
// Leave dates are set only for on_leave and cleared otherwise; the status
// note is cleared for active and otherwise kept (status sets it from
// --note on top of this patch).
func statusPatch(cur row.Row, to string, on, leaveFrom, leaveUntil time.Time) (map[string]value.Value, error) {
	st, err := textValue(schema.FieldActorStatus, to)
	if err != nil {
		return nil, err
	}
	p := map[string]value.Value{schema.FieldActorStatus: st,
		schema.FieldActorLeaveFrom: value.Null{}, schema.FieldActorLeaveUntil: value.Null{}}
	switch to {
	case schema.ActorStatusActive, schema.ActorStatusOnLeave:
		p[schema.FieldActorActive] = value.BoolOf(true)
		p[schema.FieldActorLeftAt] = value.Null{}
		if to == schema.ActorStatusActive {
			// 3.4: the status note says why someone is not active, so
			// being active again clears it (rejoin and status active).
			p[schema.FieldActorStatusNote] = value.Null{}
		}
	default:
		p[schema.FieldActorActive] = value.BoolOf(false)
		if text(cur, schema.FieldActorLeftAt) == "" || teamprofile.Pickable(teamprofile.StatusOf(cur.Fields)) {
			p[schema.FieldActorLeftAt] = value.DateTimeFrom(teamprofile.Day(on))
		}
	}
	if to == schema.ActorStatusOnLeave {
		if p[schema.FieldActorLeaveFrom], err = dateValue("--from", leaveFrom); err != nil {
			return nil, err
		}
		if !leaveUntil.IsZero() {
			if p[schema.FieldActorLeaveUntil], err = dateValue("--until", leaveUntil); err != nil {
				return nil, err
			}
		}
	}
	return p, nil
}

// status is actor.status: it writes only the actor row (no period, no
// membership; 5.3).
func (o ops) status(ctx context.Context, in ActorStatusIn) (ActorStatusOut, error) {
	to := strings.TrimSpace(in.To)
	if to != schema.ActorStatusOnLeave && (in.From != "" || in.Until != "") {
		return ActorStatusOut{}, errs.New(errs.InvalidInput, "--from and --until go only with on_leave")
	}
	note := strings.TrimSpace(in.Note)
	if note != "" && to == schema.ActorStatusActive {
		return ActorStatusOut{}, errs.New(errs.InvalidInput, "--note says why someone is not active; going active clears it").
			WithHint("drop --note, or give it with on_leave, left or archived")
	}
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return ActorStatusOut{}, err
	}
	a, err := r.ResolveActor(ctx, in.Actor)
	if err != nil {
		return ActorStatusOut{}, err
	}
	from := teamprofile.StatusOf(a.Fields)
	out := ActorStatusOut{ID: a.ID.String(), Name: nameOf(a), From: from, To: to, Where: r.Where()}
	if to != schema.ActorStatusActive {
		out.Note = text(a, schema.FieldActorStatusNote)
	}
	mv, err := statusMove(isHuman(a), from, to)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) && e.Code == errs.StatusChange {
			return ActorStatusOut{}, errs.New(errs.StatusChange, nameOf(a)+" is "+from+"; "+e.Message).WithHint(strings.ReplaceAll(e.Hint, "<actor>", in.Actor))
		}
		return ActorStatusOut{}, err
	}
	if mv == moveNoop && (note == "" || note == out.Note) {
		return out, nil
	}
	if mv == moveNoop {
		// Already there with another note (left → left --note …): only
		// the note is written.
		nv, nerr := textValue("--note", note)
		if nerr != nil {
			return ActorStatusOut{}, nerr
		}
		if _, err = r.Update(ctx, schema.TableActors, a.ID, map[string]value.Value{schema.FieldActorStatusNote: nv}); err != nil {
			return ActorStatusOut{}, err
		}
		r.Push(ctx)
		out.Note, out.Changed = note, true
		out.Warnings = r.Warnings()
		return out, nil
	}
	today := r.Today()
	var lf, lu time.Time
	if to == schema.ActorStatusOnLeave {
		if lf, err = day("from", in.From, today); err != nil {
			return ActorStatusOut{}, err
		}
		if lu, err = day("until", in.Until, time.Time{}); err != nil {
			return ActorStatusOut{}, err
		}
		if !lu.IsZero() && lu.Before(lf) {
			return ActorStatusOut{}, errs.New(errs.InvalidInput, "--until "+dayText(lu)+" is before --from "+dayText(lf))
		}
		out.LeaveFrom, out.LeaveUntil = dayText(lf), dayText(lu)
	}
	patch, err := statusPatch(a, to, today, lf, lu)
	if err != nil {
		return ActorStatusOut{}, err
	}
	if note != "" {
		if patch[schema.FieldActorStatusNote], err = textValue("--note", note); err != nil {
			return ActorStatusOut{}, err
		}
		out.Note = note
	}
	if _, err = r.Update(ctx, schema.TableActors, a.ID, patch); err != nil {
		return ActorStatusOut{}, err
	}
	r.Push(ctx)
	out.Changed = true
	out.Warnings = r.Warnings()
	return out, nil
}
