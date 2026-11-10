package task

// people.go — assignees, lead and watchers (TASK_SYSTEM_DESIGN 14, TSQ5).
//
//	add/edit flags ─► resolveAssigns (@handle → person or team; actor refs)
//	   ─► applyPeople(cur, peopleEdit) ─► rules 14.2 ─► patch:
//	        assigned_to_actor_id (= lead, so v0.1.17 readers see the lead)
//	        assignees (⊇ {lead}), watchers (+ auto watch, tasks.auto_watch)
//	task watch|unwatch ─► watchers ± me
//
// Rules (14.2): the lead is always an assignee; the first --assign on a
// task with no lead becomes the lead; removing the lead needs --lead in the
// same command (E_INVALID_INPUT); removing the last assignee clears the
// lead. A team is never a lead (assigned_team stays its own field).

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Watch operation names (14.3).
const (
	OpWatch   = "task.watch"
	OpUnwatch = "task.unwatch"
)

// People fields (schema.TableTasks).
const (
	fieldAssignees = schema.FieldTaskAssignees
	fieldWatchers  = schema.FieldTaskWatchers
)

// leadMark follows the lead's name in the human lines.
const leadMark = " (lead)"

// people is a task's lead, assignees and watchers as actor id texts.
type people struct {
	lead      string
	assignees []string
	watchers  []string
}

// peopleOf reads r's people. A lead missing from the list (an old binary
// wrote only assigned_to_actor_id, or a merge removed it) reads as an
// assignee (14.2 reader rule).
func peopleOf(r row.Row) people {
	p := people{lead: work.Field(r, fieldAssigned), assignees: work.List(r, fieldAssignees), watchers: work.List(r, fieldWatchers)}
	if p.lead != "" && !slices.Contains(p.assignees, p.lead) {
		p.assignees = append([]string{p.lead}, p.assignees...)
	}
	return p
}

// assigns is a resolved --assign list: people in order, at most one team.
type assigns struct {
	persons []string
	team    string
	given   bool
}

// first is the v0.1.17 single assignee (for the team leak check).
func (a assigns) first() work.Assignee { return work.Assignee{TeamID: a.team} }

// flagList splits repeatable, comma separated flag values.
func flagList(raw []string) []string {
	var out []string
	for _, r := range raw {
		for x := range strings.SplitSeq(r, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	}
	return out
}

// resolveAssigns resolves every --assign value (resolveAssign: a person or
// a team for an @handle, else an actor reference).
func (o ops) resolveAssigns(ctx context.Context, raw []string) (assigns, error) {
	var a assigns
	for _, r := range flagList(raw) {
		a.given = true
		got, err := o.resolveAssign(ctx, r)
		if err != nil {
			return assigns{}, err
		}
		switch {
		case got.TeamID != "" && a.team != "" && a.team != got.TeamID:
			return assigns{}, errs.New(errs.InvalidInput, "--assign names two teams ("+a.team+", "+got.TeamID+"); a task has one assigned team")
		case got.TeamID != "":
			a.team = got.TeamID
		case !slices.Contains(a.persons, got.ActorID):
			a.persons = append(a.persons, got.ActorID)
		}
	}
	return a, nil
}

// resolvePersons resolves actor references that must be people (--lead,
// --add-assignee, watchers): a team handle is refused.
func (o ops) resolvePersons(ctx context.Context, flag string, raw []string) ([]string, error) {
	var out []string
	for _, r := range flagList(raw) {
		got, err := o.resolveAssign(ctx, r)
		if err != nil {
			return nil, err
		}
		if got.TeamID != "" {
			return nil, errs.New(errs.InvalidInput, "--"+flag+" "+r+" is a team; only a person or agent actor can be named here").
				WithHint("assign a team with --assign @team")
		}
		if !slices.Contains(out, got.ActorID) {
			out = append(out, got.ActorID)
		}
	}
	return out, nil
}

// peopleEdit is the people flags of one add or edit.
type peopleEdit struct {
	assign                assigns
	lead                  string // --lead or --assigned-to
	add, remove           []string
	addWatch, removeWatch []string
	clear                 bool   // --clear-assignee
	creator               string // add only: the creator watches (14.4)
	autoWatch             bool
}

// applyPeople applies e to cur's people (zero row on add) and writes the
// changed fields into patch; summary is the human line ("" = unchanged).
func (o ops) applyPeople(ctx context.Context, cur row.Row, e peopleEdit, patch map[string]value.Value) (string, error) {
	was := peopleOf(cur)
	p := people{lead: was.lead, assignees: slices.Clone(was.assignees), watchers: slices.Clone(was.watchers)}
	if e.clear {
		p.lead, p.assignees = "", nil
	}
	if e.assign.given {
		// --assign sets the list (14.3); the first person leads unless --lead.
		p.assignees = slices.Clone(e.assign.persons)
		p.lead = ""
		if len(p.assignees) > 0 {
			p.lead = p.assignees[0]
		}
		teamV, err := textOrNull(e.assign.team)
		if err != nil {
			return "", err
		}
		if e.assign.team != "" || len(e.assign.persons) > 0 {
			// v0.1.17: a person clears the team and a team the person,
			// unless both are given together.
			patch[fieldTeam] = teamV
		}
	}
	for _, a := range e.add {
		if !slices.Contains(p.assignees, a) {
			p.assignees = append(p.assignees, a)
		}
		if p.lead == "" {
			p.lead = a
		}
	}
	for _, r := range e.remove {
		if r == p.lead && e.lead == "" && len(p.assignees) > 1 {
			return "", errs.New(errs.InvalidInput, o.actorName(ctx, r)+" is the lead of "+cur.ID.String()+". Name a new lead in the same command").
				WithHint("lore task edit " + cur.ID.String() + " --remove-assignee " + r + " --lead <actor>")
		}
		p.assignees = slices.DeleteFunc(p.assignees, func(x string) bool { return x == r })
		if r == p.lead {
			p.lead = ""
		}
	}
	if e.lead != "" {
		if !slices.Contains(p.assignees, e.lead) {
			p.assignees = append(p.assignees, e.lead)
		}
		p.lead = e.lead
	}
	if len(p.assignees) == 0 {
		p.lead = ""
	}
	if e.autoWatch {
		for _, a := range append([]string{e.creator}, p.assignees...) {
			if a != "" && !slices.Contains(was.assignees, a) && !slices.Contains(p.watchers, a) {
				p.watchers = append(p.watchers, a)
			}
		}
	}
	for _, w := range e.addWatch {
		if !slices.Contains(p.watchers, w) {
			p.watchers = append(p.watchers, w)
		}
	}
	p.watchers = slices.DeleteFunc(p.watchers, func(x string) bool { return slices.Contains(e.removeWatch, x) })
	changed := false
	if p.lead != was.lead {
		v, err := refOrNull(p.lead)
		if err != nil {
			return "", err
		}
		patch[fieldAssigned], changed = v, true
	}
	if !slices.Equal(p.assignees, work.List(cur, fieldAssignees)) && (len(p.assignees) > 0 || len(work.List(cur, fieldAssignees)) > 0) {
		v, err := textList(p.assignees)
		if err != nil {
			return "", err
		}
		patch[fieldAssignees], changed = v, true
	}
	if !slices.Equal(p.watchers, was.watchers) {
		v, err := textList(p.watchers)
		if err != nil {
			return "", err
		}
		patch[fieldWatchers] = v
		changed = changed || len(e.addWatch)+len(e.removeWatch) > 0
	}
	if !changed {
		return "", nil
	}
	return o.peopleLine(ctx, p), nil
}

// peopleLine is "assignees: mira (lead), tomas · watchers: mira, tomas".
func (o ops) peopleLine(ctx context.Context, p people) string {
	names := make([]string, 0, len(p.assignees))
	for _, a := range p.assignees {
		n := o.actorName(ctx, a)
		if a == p.lead {
			n += leadMark
		}
		names = append(names, n)
	}
	line := "assignees: " + orNone(strings.Join(names, ", "))
	if len(p.watchers) > 0 {
		ws := make([]string, 0, len(p.watchers))
		for _, w := range p.watchers {
			ws = append(ws, o.actorName(ctx, w))
		}
		line += " · watchers: " + strings.Join(ws, ", ")
	}
	return line
}

// orNone is s, or "none" when empty.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// textList is a list value of actor id texts (null when empty).
func textList(xs []string) (value.Value, error) {
	if len(xs) == 0 {
		return value.Null{}, nil
	}
	items := make([]value.Value, 0, len(xs))
	for _, x := range xs {
		t, err := value.TextOf(x)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task: people list", err)
	}
	return l, nil
}

// textOrNull is a text value, or null for "".
func textOrNull(s string) (value.Value, error) {
	if s == "" {
		return value.Null{}, nil
	}
	return value.TextOf(s)
}

// autoWatch is the effective tasks.auto_watch for target (true by default).
func (o ops) autoWatch(ctx context.Context, target store.Source) (bool, error) {
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return true, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return false, err
	}
	if e, isSet := set.Get(policy.KeyTasksAutoWatch); isSet && e.Value != nil {
		return e.Value.String() != "false", nil
	}
	return true, nil
}

// WatchIn is the input of task.watch and task.unwatch.
type WatchIn struct {
	ID string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
}

// watchOps are task watch / unwatch, appended to Ops.
func watchOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpWatch, Examples: []op.Example{{Title: "watch a task", CLI: "lore task watch tsk_<id>", Want: ""}}, Summary: "Watch a task (changes reach your inbox)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, func(ctx context.Context, in WatchIn) (work.WriteOut, error) { return o.watch(ctx, in, true) }),
		op.Define(op.Spec{
			Name: OpUnwatch, Examples: []op.Example{{Title: "stop watching", CLI: "lore task unwatch tsk_<id>", Want: ""}}, Summary: "Stop watching a task",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, func(ctx context.Context, in WatchIn) (work.WriteOut, error) { return o.watch(ctx, in, false) }),
	}
}

// watch adds or removes the caller from a task's watchers.
func (o ops) watch(ctx context.Context, in WatchIn, on bool) (work.WriteOut, error) {
	me := o.by(ctx)
	if me == "" {
		return work.WriteOut{}, errs.New(errs.Unsupported, "task watch: the current identity is not known here")
	}
	id, err := taskID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	src, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	ws := work.List(cur, fieldWatchers)
	verb := "watching"
	if on {
		if slices.Contains(ws, me) {
			return work.WriteOut{ID: id.String(), Store: string(src), Human: "already watching " + id.String() + "\n"}, nil
		}
		ws = append(ws, me)
	} else {
		verb = "stopped watching"
		if !slices.Contains(ws, me) {
			return work.WriteOut{ID: id.String(), Store: string(src), Human: "not watching " + id.String() + "\n"}, nil
		}
		ws = slices.DeleteFunc(ws, func(x string) bool { return x == me })
	}
	v, err := textList(ws)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Patch(ctx, o.hist(), Noun, table, id, map[string]value.Value{fieldWatchers: v}, work.Success+" "+verb+" "+id.String()+"\n")
}

// leadIs reports whether actor leads r (list --lead).
func leadIs(r row.Row, actor string) bool { return work.Field(r, fieldAssigned) == actor }

// assignedTo reports whether actor is r's lead or an assignee.
func assignedTo(r row.Row, actor string) bool { return slices.Contains(peopleOf(r).assignees, actor) }

// peopleFlags resolves the people flags of add/edit into a peopleEdit.
// --assigned-to (v0.1.17, an actor id) names the lead like --lead.
func (o ops) peopleFlags(ctx context.Context, a assigns, lead, assignedTo string, add, remove, addW, removeW []string) (peopleEdit, error) {
	e := peopleEdit{assign: a}
	var err error
	for _, l := range []struct{ flag, raw string }{{"assigned-to", assignedTo}, {"lead", lead}} {
		if l.raw == "" {
			continue
		}
		got, rerr := o.resolvePersons(ctx, l.flag, []string{l.raw})
		if rerr != nil {
			return peopleEdit{}, rerr
		}
		e.lead = got[0]
	}
	if e.add, err = o.resolvePersons(ctx, "add-assignee", add); err != nil {
		return peopleEdit{}, err
	}
	if e.remove, err = o.resolvePersons(ctx, "remove-assignee", remove); err != nil {
		return peopleEdit{}, err
	}
	if e.addWatch, err = o.resolvePersons(ctx, "add-watcher", addW); err != nil {
		return peopleEdit{}, err
	}
	if e.removeWatch, err = o.resolvePersons(ctx, "remove-watcher", removeW); err != nil {
		return peopleEdit{}, err
	}
	return e, nil
}

// showMore fills show's people, checklist and estimate (7.3, 9.3, 14.3).
func (o ops) showMore(ctx context.Context, full *Full, src store.Source, r row.Row) error {
	p := peopleOf(r)
	full.Lead, full.Assignees, full.Watchers = p.lead, p.assignees, p.watchers
	// OV12-3: the human line only when someone is assigned; the creator's
	// auto watch alone would add a line to every v0.1.17 show (--json has it).
	if len(p.assignees) > 0 {
		full.PeopleLine = o.peopleLine(ctx, p)
	}
	full.Checklist = checklistOut(r.ID.String(), Checklist(r)).Data.Items
	unit, err := o.estimateUnit(ctx, src)
	if err != nil {
		return err
	}
	f, err := o.forest(ctx, work.Field(r, work.FieldProject))
	if err != nil {
		return err
	}
	e := estimateInfo(f, r, unit)
	full.Estimate, full.EstimateUnit, full.EstimateSubtree, full.EstimateDone, full.EstimateOpen = e.Estimate, e.Unit, e.Subtree, e.SubtreeDone, e.SubtreeOpen
	full.Unestimated, full.TrackedSeconds, full.Ratio, full.EstimateLine = e.Unestimated, e.TrackedSeconds, e.Ratio, e.Line()
	// 18.4: the SLA on read, deferred time left out.
	hours, err := o.slaHours(ctx, src)
	if err != nil {
		return err
	}
	now := opclock.Now(o.env)
	full.SLA, full.SLALine = slaState(r, hours, now), slaLine(slaClockOf(r), hours, now)
	return nil
}
