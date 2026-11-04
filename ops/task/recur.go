package task

// recur.go — recurring tasks (TASK_SYSTEM_DESIGN 10, option C): the task
// carries its rule; closing an instance creates the next one with an id
// derived from the series and the index, so two machines that finish the
// same instance offline write the same row (10.4).
//
//	add/edit --recur ─► parseRule (logic/recur, E_RECUR) ─► recur field
//	done | recur skip ─► spawnNext:
//	    base (due day, or the completion day for every!) ─► Rule.After
//	    ─► Ends? stop : CreateWithID(tsk_ = Natural(series, index))
//	    ─► subtree re-created (ids from series, index, path; TSQ12)
//	cancel ─► no next instance (cancel means stop)
//	recur stop ─► stopped=true, this instance stays
//
// Dates are UTC calendar days everywhere (TSQ11).

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/recur"
	"github.com/thesatellite-ai/lore/model/checklist"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Recurrence operation names (10.3).
const (
	OpRecurShow = "task.recur.show"
	OpRecurEdit = "task.recur.edit"
	OpRecurSkip = "task.recur.skip"
	OpRecurStop = "task.recur.stop"
)

// fieldRecur is the recur object (schema.FieldTaskRecur).
const fieldRecur = schema.FieldTaskRecur

// upcomingShown is how many next dates create and show print (10.2).
const upcomingShown = 3

// Recur is the stored recur object (10.3).
type Recur struct {
	Rule    string `json:"rule"`
	Series  string `json:"series"`
	Index   int    `json:"index"`
	Until   string `json:"until"`
	Stopped bool   `json:"stopped"`
}

// seriesTag types rcs_ ids (not a row; the series is the set of tasks
// with the same recur.series).
type seriesTag struct{}

// Prefix implements ids.Tag.
func (seriesTag) Prefix() ids.Prefix { return ids.PrefixRecurSeries }

// IDCreator is the optional Env upgrade a next instance needs: a create
// with a pinned id (wire.App, engine.CreateWithID). Without it --recur is
// E_UNSUPPORTED rather than a series that duplicates on merge.
type IDCreator interface {
	CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error)
}

// RecurOf reads a task's recur object; false when it has none.
func RecurOf(r row.Row) (Recur, bool) {
	v := r.Fields[fieldRecur]
	if v == nil || v.Kind() == value.KindNull {
		return Recur{}, false
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return Recur{}, false
	}
	var rc Recur
	if json.Unmarshal(raw, &rc) != nil || rc.Series == "" {
		return Recur{}, false
	}
	return rc, true
}

// recurValue is rc as the stored JSON value.
func recurValue(rc Recur) (value.Value, error) {
	raw, err := json.Marshal(rc)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task: encode recur", err)
	}
	return value.JSONOf(raw)
}

// recurErr maps a logic/recur refusal to E_RECUR with the forms.
func recurErr(err error) error {
	var re *recur.Error
	if errors.As(err, &re) {
		return errs.New(errs.Recur, "--recur: "+re.Error())
	}
	return errs.Wrap(errs.Recur, "--recur", err)
}

// parseRule reads a --recur value; "until" takes the TASK 12 phrases of
// the target's clock and is stored resolved (Rule.Canonical).
func (o ops) parseRule(ctx context.Context, target store.Source, raw string) (recur.Rule, error) {
	c, err := o.clock(ctx, target)
	if err != nil {
		return recur.Rule{}, err
	}
	r, err := recur.Parse(raw, func(s string) (time.Time, error) {
		res, perr := dates.Parse(s, c)
		return res.At, perr
	})
	if err != nil {
		return recur.Rule{}, recurErr(err)
	}
	return r, nil
}

// storedRule re-reads a stored (canonical) rule: until is a plain day.
func storedRule(raw string) (recur.Rule, error) {
	r, err := recur.Parse(raw, func(s string) (time.Time, error) { return time.Parse(dates.DayLayout, s) })
	if err != nil {
		return recur.Rule{}, recurErr(err)
	}
	return r, nil
}

// dayList prints days as "2026-11-30, 2026-12-31".
func dayList(ds []time.Time) string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Format(dates.DayLayout)
	}
	return strings.Join(out, ", ")
}

// setRecur writes the recur field of fields for rule raw: a task without
// a series starts one (index 1); one with a series keeps it (recur edit
// applies to this and later instances). When fields has no due date a
// fixed-day rule takes its first day on or after today, an interval rule
// today (DECISION OV15C-1). It returns the "recurs:" line.
func (o ops) setRecur(ctx context.Context, target store.Source, fields map[string]value.Value, cur Recur, due time.Time, raw string) (string, error) {
	rule, err := o.parseRule(ctx, target, raw)
	if err != nil {
		return "", err
	}
	rc := cur
	if rc.Series == "" {
		sid, nerr := ids.New[seriesTag]()
		if nerr != nil {
			return "", nerr
		}
		rc = Recur{Series: sid.String(), Index: 1}
	}
	rc.Rule, rc.Stopped, rc.Until = rule.Canonical(), false, ""
	if !rule.Until.IsZero() {
		rc.Until = rule.Until.Format(dates.DayLayout)
	}
	if fields[fieldRecur], err = recurValue(rc); err != nil {
		return "", err
	}
	if due.IsZero() {
		today := recur.Day(opclock.Now(o.env))
		due = today
		if rule.Unit == "" {
			due = rule.Next(today.AddDate(0, 0, -1))
		}
		fields[fieldDue] = value.DateTimeFrom(due)
	}
	return "recurs: " + rc.Rule + " → next: " + dayList(rule.Upcoming(due, upcomingShown)) + " (series " + rc.Series + ", #" + strconv.Itoa(rc.Index) + ")", nil
}

// instanceFields are the fields a next instance copies (10.4). Not
// copied: claims, time, history, comments, relations, dependencies.
var instanceFields = []string{
	work.FieldTitle, work.FieldBody, fieldPriority, fieldCommitment, work.FieldProject, work.FieldRepo,
	work.FieldMission, work.FieldTasklist, work.FieldPlan, fieldAssigned, fieldAssignees, fieldWatchers,
	fieldTeam, schema.FieldTagIDs, fieldEstimate, fieldRole, fieldTimebox, fieldGate,
}

// RecurNext is the next instance a close created (zero when none).
type RecurNext struct {
	ID       string `json:"id,omitempty"`
	Due      string `json:"due,omitempty"`
	Series   string `json:"series,omitempty"`
	Index    int    `json:"index,omitempty"`
	Skipped  int    `json:"skipped_dates,omitempty"`
	Children int    `json:"children,omitempty"`
	Existed  bool   `json:"existed,omitempty"`
	Ended    bool   `json:"series_ended,omitempty"`
}

// spawnNext creates the next instance of cur's series after cur closed at
// at (done or skipped), with its re-created subtree. A row with the
// derived id already there (the other machine finished it first, or a
// re-run) is reported, not written again.
func (o ops) spawnNext(ctx context.Context, f forest, src store.Source, cur row.Row, at time.Time) (RecurNext, string, error) {
	rc, ok := RecurOf(cur)
	if !ok || rc.Stopped {
		return RecurNext{}, "", nil
	}
	rule, err := storedRule(rc.Rule)
	if err != nil {
		return RecurNext{}, "", err
	}
	creator, ok := upgrade.As[IDCreator](o.env)
	if !ok {
		return RecurNext{}, "", errs.New(errs.Unsupported, "task: this surface cannot create the next instance of a recurring task (no pinned-id create)")
	}
	due := work.Time(cur, fieldDue)
	base, counted := recur.Day(due), "due "+recur.Day(due).Format(dates.DayLayout)
	if rule.FromDone || due.IsZero() {
		// TSQ11: every! counts from the UTC completion day; say so.
		base, counted = recur.Day(at), "counted from "+recur.Day(at).Format(dates.DayLayout)+" (UTC)"
	}
	next, skipped := rule.After(base, at)
	idx := rc.Index + 1
	if rule.Ends(idx, next) {
		return RecurNext{Series: rc.Series, Ended: true}, "  series " + rc.Series + " ended (" + rc.Rule + ")\n", nil
	}
	if !due.IsZero() {
		next = next.Add(due.Sub(recur.Day(due))) // keep a due time of day
	}
	rc.Index = idx
	out := RecurNext{Due: next.Format(dates.DayLayout), Series: rc.Series, Index: idx, Skipped: skipped}
	id, existed, err := o.instance(ctx, creator, src, cur, rc, "", "", next)
	if err != nil {
		return RecurNext{}, "", err
	}
	out.ID, out.Existed = id.String(), existed
	if !existed {
		if out.Children, err = o.copySubtree(ctx, creator, f, src, cur.ID.String(), id.String(), rc, ""); err != nil {
			return RecurNext{}, "", err
		}
	}
	human := "  " + rule.Raw + " " + counted + "\n"
	if skipped > 0 {
		human += "  skipped " + strconv.Itoa(skipped) + " past dates\n"
	}
	human += "  next instance: " + out.ID + " due " + out.Due + " (series " + rc.Series + ", #" + strconv.Itoa(idx) + ")"
	if existed {
		human += " (already there)"
	}
	if out.Children > 0 {
		human += ", " + strconv.Itoa(out.Children) + " sub-tasks"
	}
	return out, human + "\n", nil
}

// instance writes one task of the next instance rc.Index: path "" is its
// root (recur and due set), else the child at that tree path under parent.
func (o ops) instance(ctx context.Context, c IDCreator, src store.Source, from row.Row, rc Recur, path, parent string, due time.Time) (ids.AnyID, bool, error) {
	parts := []string{rc.Series, strconv.Itoa(rc.Index)}
	if path != "" {
		parts = append(parts, path)
	}
	id, err := ids.Natural(ids.PrefixTask, parts...)
	if err != nil {
		return ids.AnyID{}, false, err
	}
	fields := map[string]value.Value{}
	for _, k := range instanceFields {
		if v, ok := from.Fields[k]; ok && v != nil && v.Kind() != value.KindNull {
			fields[k] = v
		}
	}
	if _, err = o.setStatus(ctx, "", row.Row{}, schema.TaskStatusTodo, false, fields); err != nil {
		return ids.AnyID{}, false, err
	}
	if v := o.currentActorRef(ctx); v != nil {
		fields[work.FieldCreatedBy] = v
	}
	if path == "" {
		fields[fieldDue] = value.DateTimeFrom(due)
		if fields[fieldRecur], err = recurValue(rc); err != nil {
			return ids.AnyID{}, false, err
		}
	} else {
		if fields[fieldParent], err = parentRef(parent); err != nil {
			return ids.AnyID{}, false, err
		}
		if r := work.Field(from, fieldRank); r != "" {
			if fields[fieldRank], err = value.TextOf(r); err != nil {
				return ids.AnyID{}, false, err
			}
		}
	}
	if err = o.freshChecklist(ctx, fields, from, parts); err != nil {
		return ids.AnyID{}, false, err
	}
	if _, err = c.CreateWithID(ctx, src, table, id, fields); err != nil {
		if errs.Has(err, errs.Stale) {
			return id, true, nil // 10.4: the same instance, created elsewhere
		}
		return ids.AnyID{}, false, err
	}
	return id, false, nil
}

// copySubtree re-creates the children of old under new (TSQ12): each
// child's id comes from the series, the index and its tree path, statuses
// reset; children are ordered by rank, ties by id (tasktree).
func (o ops) copySubtree(ctx context.Context, c IDCreator, f forest, src store.Source, old, parent string, rc Recur, path string) (int, error) {
	n := 0
	for i, kid := range f.tree.Children(old) {
		s := f.row(kid)
		if s.Row.Deleted {
			continue
		}
		p := strconv.Itoa(i)
		if path != "" {
			p = path + "." + p
		}
		kidID, existed, err := o.instance(ctx, c, src, s.Row, rc, p, parent, time.Time{})
		if err != nil {
			return n, err
		}
		if existed {
			continue
		}
		n++
		m, err := o.copySubtree(ctx, c, f, src, kid, kidID.String(), rc, p)
		if err != nil {
			return n, err
		}
		n += m
	}
	return n, nil
}

// freshChecklist copies from's live checklist unticked, item ids derived
// from the instance key parts and the item position (10.4).
func (o ops) freshChecklist(ctx context.Context, fields map[string]value.Value, from row.Row, parts []string) error {
	live := checklist.Live(Checklist(from))
	if len(live) == 0 {
		return nil
	}
	by, stamp := o.by(ctx), opclock.Now(o.env).UTC().Format(checklist.TimeLayout)
	list := make([]checklist.Item, 0, len(live))
	for i, it := range live {
		aid, err := ids.Natural(ids.PrefixChecklistItem, append(slices.Clone(parts), "chk", strconv.Itoa(i))...)
		if err != nil {
			return err
		}
		cid, err := ids.As[checklist.Tag](aid)
		if err != nil {
			return err
		}
		list = append(list, checklist.Item{ID: cid, Text: it.Text, Rank: it.Rank, CreatedAt: stamp, CreatedBy: by, UpdatedAt: stamp, UpdatedBy: by})
	}
	return putChecklist(fields, list)
}
