package task

// recur_ops.go — `lore task recur show|edit|skip|stop` (TASK 10.3).
//
//	show ─► RecurOf + the next dates from the due day
//	edit ─► editRecur (series and index kept) ─► Patch
//	skip ─► close as cancelled/skipped (sub-tasks too) ─► spawnNext
//	stop ─► confirm ─► stopped=true (this instance stays open)

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// RecurEditIn is the input of task.recur.edit.
type RecurEditIn struct {
	ID string `json:"id" arg:"0" desc:"an instance of the series (tsk_…)"`
	// Rule is checked by the handler after the instance lookup (omitempty),
	// so a bad id answers E_NOT_FOUND first (ops/internal/lookup).
	Rule string `json:"rule,omitempty" arg:"1" rest:"true" desc:"the new rule, e.g. \"every! 30 days\" (this and later instances)"`
}

// RecurOut is the output of the recur operations.
type RecurOut struct {
	ID       string    `json:"id"`
	Recur    Recur     `json:"recur"`
	Upcoming []string  `json:"upcoming,omitempty"`
	Next     RecurNext `json:"next,omitzero"`
	Human    string    `json:"-"`
}

// Text implements op.Texter.
func (o RecurOut) Text() string { return o.Human }

// recurOps are the recur operations.
func recurOps(o ops) []op.Entry {
	w := append(slices.Clone(work.WriteErrors), errs.Recur, errs.Unsupported)
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRecurShow, Summary: "Show a recurring task's rule, series and next dates",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(work.ReadErrors), errs.Recur),
		}, o.recurShow),
		op.Define(op.Spec{
			Name: OpRecurEdit, Summary: "Change the recurrence rule (this and later instances)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
			Examples: []op.Example{{Title: "count from completion", CLI: `lore task recur edit tsk_<id> "every! 30 days"`, Want: "recurs"}},
		}, o.recurEdit),
		op.Define(op.Spec{
			Name: OpRecurSkip, Summary: "Close this instance as skipped and create the next one",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(w, errs.OpenChildren),
		}, o.recurSkip),
		op.Define(op.Spec{
			Name: OpRecurStop, Summary: "Stop the series: no further instances (this one stays)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
			Confirm: op.ConfirmRule{Always: true},
		}, o.recurStop),
	}
}

// recurring reads task id and its recur object (E_RECUR when it has none).
func (o ops) recurring(ctx context.Context, raw string) (store.Source, row.Row, Recur, error) {
	id, err := taskID(raw)
	if err != nil {
		return "", row.Row{}, Recur{}, err
	}
	src, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return "", row.Row{}, Recur{}, err
	}
	rc, ok := RecurOf(cur)
	if !ok {
		return "", row.Row{}, Recur{}, errs.New(errs.Recur, "task "+id.String()+" does not recur").
			WithHint(`lore task edit ` + id.String() + ` --recur "every week"`)
	}
	return src, cur, rc, nil
}

// recurShow prints the rule, the series and the next dates.
func (o ops) recurShow(ctx context.Context, in IDIn) (RecurOut, error) {
	_, cur, rc, err := o.recurring(ctx, in.ID)
	if err != nil {
		return RecurOut{}, err
	}
	rule, err := storedRule(rc.Rule)
	if err != nil {
		return RecurOut{}, err
	}
	out := RecurOut{ID: cur.ID.String(), Recur: rc}
	if due := work.Time(cur, fieldDue); !due.IsZero() && !rc.Stopped {
		for _, d := range rule.Upcoming(due, upcomingShown) {
			out.Upcoming = append(out.Upcoming, d.Format(time.DateOnly))
		}
	}
	state := "active"
	if rc.Stopped {
		state = "stopped"
	}
	out.Human = cur.ID.String() + " " + work.Field(cur, work.FieldTitle) + "\n  rule: " + rc.Rule + "\n  series: " + rc.Series + " #" + strconv.Itoa(rc.Index) + " (" + state + ")\n"
	if len(out.Upcoming) > 0 {
		out.Human += "  next: " + strings.Join(out.Upcoming, ", ") + "\n"
	}
	return out, nil
}

// editRecur sets a new rule on cur in patch (series and index kept, a
// stopped series restarts); used by task edit --recur and recur edit.
func (o ops) editRecur(ctx context.Context, src store.Source, cur row.Row, patch map[string]value.Value, raw string) (string, error) {
	rc, _ := RecurOf(cur)
	due := work.Time(cur, fieldDue)
	if v, ok := patch[fieldDue]; ok {
		due = work.Time(row.Row{Fields: map[string]value.Value{fieldDue: v}}, fieldDue)
	}
	return o.setRecur(ctx, src, patch, rc, due, raw)
}

// recurEdit is task.recur.edit.
func (o ops) recurEdit(ctx context.Context, in RecurEditIn) (RecurOut, error) {
	src, cur, _, err := o.recurring(ctx, in.ID)
	if err != nil {
		return RecurOut{}, err
	}
	if err := requireField("rule", in.Rule); err != nil {
		return RecurOut{}, err
	}
	patch := map[string]value.Value{}
	line, err := o.editRecur(ctx, src, cur, patch, in.Rule)
	if err != nil {
		return RecurOut{}, err
	}
	w, err := work.Patch(ctx, o.hist(), Noun, table, cur.ID, patch, work.Success+" "+cur.ID.String()+" updated\n  "+line+"\n")
	if err != nil {
		return RecurOut{}, err
	}
	rc, _ := RecurOf(row.Row{Fields: patch})
	return RecurOut{ID: w.ID, Recur: rc, Human: w.Human}, nil
}

// recurSkip closes the instance as skipped (its open sub-tasks cancelled
// in the same batch) and creates the next one (10.3).
func (o ops) recurSkip(ctx context.Context, in IDIn) (RecurOut, error) {
	src, cur, rc, err := o.recurring(ctx, in.ID)
	if err != nil {
		return RecurOut{}, err
	}
	if rc.Stopped {
		return RecurOut{}, errs.New(errs.Recur, "series "+rc.Series+" is stopped: nothing to skip to").
			WithHint("lore task cancel " + cur.ID.String())
	}
	ctx, f, childLines, err := o.closeTree(ctx, cur.ID, "skip", childrenCancel, childrenCancel)
	if err != nil {
		return RecurOut{}, err
	}
	patch, err := o.closePatch(ctx, src, cur, schema.TaskStatusCancelled, schema.TaskCloseSkipped)
	if err != nil {
		return RecurOut{}, err
	}
	w, err := work.Patch(ctx, o.hist(), Noun, table, cur.ID, patch, work.Success+" "+cur.ID.String()+" skipped\n")
	if err != nil {
		return RecurOut{}, err
	}
	next, line, err := o.spawnNext(ctx, f, src, cur, opclock.Now(o.env))
	if err != nil {
		return RecurOut{}, err
	}
	return RecurOut{ID: w.ID, Recur: rc, Next: next, Human: w.Human + childLines + line}, nil
}

// recurStop ends the series: no further instances; this one stays.
func (o ops) recurStop(ctx context.Context, in IDIn) (RecurOut, error) {
	_, cur, rc, err := o.recurring(ctx, in.ID)
	if err != nil {
		return RecurOut{}, err
	}
	rc.Stopped = true
	v, err := recurValue(rc)
	if err != nil {
		return RecurOut{}, err
	}
	w, err := work.Patch(ctx, o.hist(), Noun, table, cur.ID, map[string]value.Value{fieldRecur: v},
		work.Success+" "+cur.ID.String()+" series "+rc.Series+" stopped (no further instances; this one stays)\n")
	if err != nil {
		return RecurOut{}, err
	}
	return RecurOut{ID: w.ID, Recur: rc, Human: w.Human}, nil
}

// inSeries reports a task of series sid (list --series).
func inSeries(r row.Row, sid string) bool {
	rc, ok := RecurOf(r)
	return ok && rc.Series == sid
}
