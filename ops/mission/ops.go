// Package mission defines the `mission` noun's operations on the new core:
// add, list, show, edit, done, pause, resume, delete and search, with
// v0.1.17's flags and output. `mission status` (claims, gates, the ready
// queue) reads the signals ref and stays on the old binary for now; the
// YAML import `mission apply` (TSQ9, formerly `plan apply`) is not on the
// core yet.
package mission

import (
	"context"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/creator"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/ops/internal/wheredel"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (op/route.go: mission.add → `lore mission add`, POST
// /v1/missions).
const (
	// Noun is the CLI noun of every mission operation.
	Noun     = "mission"
	OpAdd    = "mission.add"
	OpList   = "mission.list"
	OpShow   = "mission.show"
	OpEdit   = "mission.edit"
	OpDone   = "mission.done"
	OpPause  = "mission.pause"
	OpResume = "mission.resume"
	OpDelete = "mission.delete"
	OpSearch = "mission.search"
)

// Mission field names (schema.TableMissions).
const (
	fieldTarget    = "target_date"
	fieldStartsAt  = "starts_at"
	fieldApprovers = "approvers"
)

// table is the missions table.
const table row.Table = schema.TableMissions

// Ops returns every mission operation bound to env.
func Ops(env work.Env) []op.Entry {
	o := ops{env: env}
	status := func(name, summary, to, label string, ex op.Example) op.Entry {
		return op.Define(op.Spec{Name: name, Summary: summary, Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors, Examples: []op.Example{ex}},
			func(ctx context.Context, in IDIn) (work.WriteOut, error) { return o.setStatus(ctx, in.ID, to, label) })
	}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new mission",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.Unsupported),
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a mission", CLI: `lore mission add "Ship the invite flow" --target 2026-12-31`, Want: "msn_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Examples: []op.Example{{Title: "list as JSON", CLI: "lore mission list --json", Want: `"data"`}}, Summary: "List missions (with attached tasks in --json mode)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Examples: []op.Example{{Title: "show as JSON", CLI: "lore mission show msn_<id> --json", Want: `"data"`}}, Summary: "Show mission details + attached tasks",
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(work.ReadErrors), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Examples: []op.Example{{Title: "move the target date", CLI: "lore mission edit msn_<id> --target 2027-01-31", Want: "msn_"}}, Summary: "Edit mission fields (only flags you pass are applied)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDone, Examples: []op.Example{{Title: "finish a mission", CLI: "lore mission done msn_<id>", Want: "done"}}, Summary: "Mark mission as done",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, o.done),
		status(OpPause, "Mark mission as paused", schema.MissionStatusPaused, "paused",
			op.Example{Title: "put a mission on hold", CLI: "lore mission pause msn_12"}),
		status(OpResume, "Mark mission as active (resume from paused)", schema.MissionStatusActive, "resumed",
			op.Example{Title: "pick a paused mission up again", CLI: "lore mission resume msn_12"}),
		op.Define(op.Spec{
			Name: OpDelete, Examples: []op.Example{{Title: "preview a bulk delete", CLI: "lore mission delete --where status=done --dry-run", Want: "would"}}, Summary: "Delete a mission (its tasks go with it: one batch, to the trash)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors,
			Confirm: op.ConfirmRule{MayAsk: true}, // asked in delete: --where --dry-run never asks, FA-LTASK-2
		}, o.delete),
		applyOp(o),
		op.Define(op.Spec{
			Name: OpSearch, Examples: []op.Example{{Title: "find missions", CLI: `lore mission search "invite"`, Want: "msn_"}}, Summary: "Full-text search across mission title + body + status",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.SearchErrors,
		}, o.search),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env work.Env }

// missionID parses a mission id argument.
func missionID(s string) (ids.AnyID, error) { return work.ParseID(Noun, ids.PrefixMission, s) }

// add creates one mission (or, with --key, finds the one already added).
func (o ops) add(ctx context.Context, in AddIn) (ShowOut, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "mission add: the title is empty").WithHint(`lore mission add "<title>"`)
	}
	project, err := work.Project(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ShowOut{}, err
	}
	if in.Key != "" {
		all, lerr := work.ListAll(ctx, o.env, table, "")
		if lerr != nil {
			return ShowOut{}, lerr
		}
		for _, s := range all {
			if work.Field(s.Row, work.FieldKey) == in.Key && work.Field(s.Row, work.FieldProject) == project.String() {
				out, serr := o.showOf(ctx, s.Source, s.Row)
				out.Kind = KindAdd
				out.Human = work.Success + " " + s.Row.ID.String() + " — " + work.Field(s.Row, work.FieldTitle) + " (already added with --key " + in.Key + ")\n"
				return out, serr
			}
		}
	}
	fields, err := work.Texts(Noun, map[string]string{
		work.FieldTitle: title, work.FieldBody: in.Body, work.FieldStatus: schema.MissionStatusActive, work.FieldKey: in.Key,
	})
	if err != nil {
		return ShowOut{}, err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return ShowOut{}, err
	}
	notes, err := o.setDates(ctx, store.Source(in.To), fields, in.Target, in.StartsAt)
	if err != nil {
		return ShowOut{}, err
	}
	for _, rf := range []struct {
		field, flag string
		p           ids.Prefix
		raw         string
	}{{work.FieldPlan, "plan", ids.PrefixPlan, in.Plan}} {
		v, rerr := work.RefTo(Noun, rf.flag, rf.p, rf.raw)
		if rerr != nil {
			return ShowOut{}, rerr
		}
		if v != nil {
			fields[rf.field] = v
		}
	}
	if v := creator.Ref(ctx, o.env); v != nil {
		fields[work.FieldCreatedBy] = v // the calling actor, never caller-set (INTEG-AUDIT-1)
	}
	if len(in.Approvers) > 0 {
		if fields[fieldApprovers], err = approvers(ctx, in.Approvers); err != nil {
			return ShowOut{}, err
		}
	}
	src, w, err := work.Create(ctx, o.env, in.To, table, fields)
	if err != nil {
		return ShowOut{}, err
	}
	out, err := o.showOf(ctx, src, w.Row)
	out.Kind = KindAdd
	label := ""
	if l := work.Label(work.LabelerOf(o.env), src); l != "" {
		label = " → " + l // v0.1.17: " → " + route.Label ("[northwind/acme]")
	}
	out.Human = work.Success + " " + w.Row.ID.String() + " — " + title + label + "\n" + noteLines(notes)
	return out, err
}

// setDates sets target_date and starts_at from TASK 12 date phrases
// (ops/task DateValueFor: tasks.week_end of the target store, TASK 12.1);
// notes say what each phrase stored (R12).
func (o ops) setDates(ctx context.Context, to store.Source, fields map[string]value.Value, target, starts string) ([]string, error) {
	var notes []string
	for _, d := range []struct{ field, flag, raw string }{{fieldTarget, "target", target}, {fieldStartsAt, "starts-at", starts}} {
		if strings.TrimSpace(d.raw) == "" {
			continue
		}
		v, note, err := opstask.DateValueFor(ctx, o.env, to, d.flag, d.raw)
		if err != nil {
			return nil, err
		}
		fields[d.field] = v
		if note != "" {
			notes = append(notes, note)
		}
	}
	return notes, nil
}

// noteLines prints the date notes, one indented line each.
func noteLines(notes []string) string {
	var b strings.Builder
	for _, n := range notes {
		b.WriteString("  " + n + "\n")
	}
	return b.String()
}

// approvers is the approvers list of act_ ids (comma separated or
// repeated). An @handle resolves to a person through the host's work.Team
// (the roster); without one it is refused, never guessed.
func approvers(ctx context.Context, raw []string) (value.Value, error) {
	var items []value.Value
	for _, r := range raw {
		for _, a := range strings.Split(r, ",") {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			if work.IsHandle(a) {
				t, ok := work.TeamFrom(ctx)
				if !ok {
					return nil, errs.New(errs.Unsupported, "mission: --approvers "+a+": @handles need the team roster, which this surface does not provide").
						WithHint("name the approver by actor id (act_…)")
				}
				who, err := t.ResolveHandle(ctx, a, false)
				if err != nil {
					return nil, err
				}
				a = who.ActorID
			}
			if _, err := ids.Parse[ids.Actor](a); err != nil {
				return nil, lookup.BadID("mission: --approvers "+work.Quote(a)+" is not an actor id (act_…)", err)
			}
			t, err := value.TextOf(a)
			if err != nil {
				return nil, err
			}
			items = append(items, t)
		}
	}
	if len(items) == 0 {
		return value.Null{}, nil
	}
	return value.ListOf(value.KindText, items...)
}

// tasksOf are the live tasks of mission id, in id order, as briefs.
func (o ops) tasksOf(ctx context.Context, id string) ([]opstask.Brief, error) {
	all, err := work.ListAll(ctx, o.env, schema.TableTasks, "")
	if err != nil {
		return nil, err
	}
	out := []opstask.Brief{}
	for _, s := range all {
		if work.Field(s.Row, work.FieldMission) == id {
			out = append(out, opstask.BriefOf(work.LabelerOf(o.env), s.Source, s.Row))
		}
	}
	return out, nil
}

// missionRows are the live task rows of mission id (the rollup input).
func (o ops) missionRows(ctx context.Context, id string) ([]row.Row, error) {
	all, err := work.ListAll(ctx, o.env, schema.TableTasks, "")
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, s := range all {
		if !s.Row.Deleted && work.Field(s.Row, work.FieldMission) == id {
			out = append(out, s.Row)
		}
	}
	return out, nil
}

// showOf is the show envelope of one mission row.
func (o ops) showOf(ctx context.Context, src store.Source, r row.Row) (ShowOut, error) {
	tasks, err := o.tasksOf(ctx, r.ID.String())
	if err != nil {
		return ShowOut{}, err
	}
	// TASK 9.2/9.3: estimate rollup and tracked time over the mission's tasks.
	rows, err := o.missionRows(ctx, r.ID.String())
	if err != nil {
		return ShowOut{}, err
	}
	est, err := opstask.MissionRollup(ctx, o.env, src, rows)
	if err != nil {
		return ShowOut{}, err
	}
	rec := Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy, ProjectID: work.Field(r, work.FieldProject),
		Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody), Status: work.Field(r, work.FieldStatus),
		TargetDate: work.Time(r, fieldTarget), StartsAt: work.Time(r, fieldStartsAt), CompletedAt: work.Time(r, work.FieldCompleted),
		CreatedByActorID: work.Field(r, work.FieldCreatedBy), Approvers: work.List(r, fieldApprovers),
		IdempotencyKey: work.Field(r, work.FieldKey), PlanID: work.Field(r, work.FieldPlan), TagIDs: work.TagIDs(r),
		Tasks: tasks, Estimate: est, Store: work.SourceOf(work.LabelerOf(o.env), src), Label: work.Label(work.LabelerOf(o.env), src),
	}
	mb := metashow.Row(ctx, o.env, r) // METADATA 3.17: metadata on every read
	rec.Meta, rec.MetaMasked = mb.Meta, mb.MetaMasked
	if rec.CreatedByActorID == "" && !r.CreatedBy.IsZero() {
		rec.CreatedByActorID = r.CreatedBy.String()
	}
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Count: len(tasks), Data: rec}, nil
}

// Text implements op.Texter: the add line, or v0.1.17's mission show.
func (o ShowOut) baseText() string {
	if o.Human != "" {
		return o.Human
	}
	m := o.Data
	var b strings.Builder
	b.WriteString(m.ID + "\n  title:  " + m.Title + "\n  status: " + m.Status + "\n")
	if !m.StartsAt.IsZero() {
		b.WriteString("  starts: " + m.StartsAt.UTC().Format(work.DateLayout) + "\n")
	}
	if !m.TargetDate.IsZero() {
		b.WriteString("  target: " + m.TargetDate.UTC().Format(work.DateLayout) + "\n")
	}
	b.WriteString(m.Estimate.Lines())
	if m.Body != "" {
		b.WriteString("\n" + m.Body + "\n")
	}
	if len(m.Tasks) > 0 {
		b.WriteString("\nTasks:\n")
		for _, t := range m.Tasks {
			b.WriteString("  " + t.ID + " [" + t.Status + "] " + t.Title)
			if o.timed {
				b.WriteString("  time:" + timeentry.FormatSeconds(t.Time.EffortSeconds) + "  elapsed:" + timeentry.FormatSeconds(t.Time.ElapsedSeconds))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// list returns the missions of every attached store (open by default).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, table) // DELETION 3.2: --include-deleted / --deleted-only
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	all, err := work.ListAll(ctx, o.env, table, in.Store)
	if err != nil {
		return ListOut{}, err
	}
	// METADATA 3.7: --meta / --place / --sort meta.<key> narrow the rows.
	all, notes, err := work.Narrow(ctx, o.env, all, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: work.SchemaVersion, Kind: KindList, Data: []Summary{}, Notes: notes}
	for _, s := range all {
		st := work.Field(s.Row, work.FieldStatus)
		switch {
		case project != "" && work.Field(s.Row, work.FieldProject) != project,
			in.Status != "" && st != in.Status,
			in.Status == "" && !in.All && (st == schema.MissionStatusDone || st == schema.MissionStatusCancelled):
			continue
		}
		tasks, terr := o.tasksOf(ctx, s.Row.ID.String())
		if terr != nil {
			return ListOut{}, terr
		}
		out.Data = append(out.Data, Summary{
			ID: s.Row.ID.String(), Title: work.Field(s.Row, work.FieldTitle), Body: work.Field(s.Row, work.FieldBody), Status: st,
			TargetDate: work.TimeText(s.Row, fieldTarget), StartsAt: work.TimeText(s.Row, fieldStartsAt), CompletedAt: work.TimeText(s.Row, work.FieldCompleted),
			CreatedAt: s.Row.CreatedAt.UTC().Format(work.TimeLayout), Tasks: tasks, Store: work.SourceOf(work.LabelerOf(o.env), s.Source), Label: work.Label(work.LabelerOf(o.env), s.Source), CreatedBy: auditrec.Of(s.Row).CreatedBy, UpdatedBy: auditrec.Of(s.Row).UpdatedBy,
		})
		mb := metashow.Row(ctx, o.env, s.Row) // METADATA 3.17: list --json rows carry it
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = mb.Meta, mb.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// Text prints "<id> [<status>] <title>   (<n> tasks)" per mission.
func (o ListOut) Text() string { return o.rowsText() + work.NotesText(o.Notes) }

// rowsText is the list lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no missions)\n"
	}
	var b strings.Builder
	for _, m := range o.Data {
		b.WriteString(m.ID + " [" + m.Status + "] " + m.Title + "   (" + strconv.Itoa(len(m.Tasks)) + " tasks)\n")
	}
	return b.String()
}

// show returns one mission and its tasks.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	id, err := missionID(in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return ShowOut{}, err
	}
	return o.showOf(ctx, src, r)
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (work.WriteOut, error) {
	id, err := missionID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	if in.Status != "" {
		if serr := work.OneOf(Noun, "status", in.Status, schema.MissionStatuses()); serr != nil {
			return work.WriteOut{}, serr
		}
	}
	patch, err := work.Texts(Noun, map[string]string{work.FieldTitle: strings.TrimSpace(in.Title), work.FieldBody: in.Body, work.FieldStatus: in.Status})
	if err != nil {
		return work.WriteOut{}, err
	}
	if in.Status != "" {
		if err = o.completedFor(ctx, id, in.Status, patch); err != nil {
			return work.WriteOut{}, err
		}
	}
	notes, err := o.setDates(ctx, "", patch, in.Target, in.StartsAt)
	if err != nil {
		return work.WriteOut{}, err
	}
	if in.ClearTarget {
		patch[fieldTarget] = value.Null{}
	}
	if in.ClearStarts {
		patch[fieldStartsAt] = value.Null{}
	}
	if v, rerr := work.RefTo(Noun, "plan", ids.PrefixPlan, in.Plan); rerr != nil {
		return work.WriteOut{}, rerr
	} else if v != nil {
		patch[work.FieldPlan] = v
	}
	if in.ClearPlan {
		patch[work.FieldPlan] = value.Null{}
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return work.WriteOut{}, errs.New(errs.InvalidInput, "mission edit: nothing to change").
			WithHint("give at least one of --title, --body, --status, --target, --clear-target, --starts-at, --clear-starts-at, --plan, --clear-plan")
	}
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" "+id.String()+" updated\n"+noteLines(notes))
}

// done marks the mission done now.
func (o ops) done(ctx context.Context, in IDIn) (work.WriteOut, error) {
	id, err := missionID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	patch, err := work.Texts(Noun, map[string]string{work.FieldStatus: schema.MissionStatusDone})
	if err != nil {
		return work.WriteOut{}, err
	}
	patch[work.FieldCompleted] = work.Now(o.env)
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" "+id.String()+" completed\n")
}

// setStatus sets the mission's status (pause / resume).
func (o ops) setStatus(ctx context.Context, raw, status, label string) (work.WriteOut, error) {
	id, err := missionID(raw)
	if err != nil {
		return work.WriteOut{}, err
	}
	patch, err := work.Texts(Noun, map[string]string{work.FieldStatus: status})
	if err != nil {
		return work.WriteOut{}, err
	}
	if err = o.completedFor(ctx, id, status, patch); err != nil {
		return work.WriteOut{}, err
	}
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" "+id.String()+" "+label+"\n")
}

// completedFor keeps completed_at true to a status write: a mission that
// becomes done (by edit --status) is stamped now unless it already was
// done; one that becomes active, paused or cancelled carries no
// completion date (a resumed mission is not complete).
func (o ops) completedFor(ctx context.Context, id ids.AnyID, status string, patch map[string]value.Value) error {
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return err
	}
	prev := work.Field(cur, work.FieldStatus)
	switch {
	case status == schema.MissionStatusDone && prev != schema.MissionStatusDone:
		patch[work.FieldCompleted] = work.Now(o.env)
	case status != schema.MissionStatusDone && !work.Time(cur, work.FieldCompleted).IsZero():
		patch[work.FieldCompleted] = value.Null{}
	}
	return nil
}

// delete tombstones the mission; its tasks go with it (tasks.mission_id is
// a Cascade relation, schema/builtin_work.go), claimed ones fenced first.
func (o ops) delete(ctx context.Context, in DeleteIn) (work.WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	bulkForm, err := wheredel.Form(Noun, in.ID, in.Where, in.DryRun)
	if err != nil {
		return work.WriteOut{}, err
	}
	if bulkForm {
		res, werr := wheredel.Run(ctx, o.env, wheredel.Spec{Noun: Noun, Table: table, Fence: func(fctx context.Context, tasks []wheredel.Located) (int, error) {
			rows := make([]work.Sourced, len(tasks))
			for i, t := range tasks {
				rows[i] = work.Sourced(t)
			}
			return opstask.FenceClaimed(fctx, o.env, rows) // TASK 6.12 / FA-TASK-7
		}}, in.Where, in.DryRun)
		if werr != nil {
			return work.WriteOut{}, werr
		}
		return work.WriteOut{ID: res.Batch, Store: res.Store, Rows: res.Rows, Human: res.Human}, nil
	}
	id, err := missionID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	// a missing mission is E_NOT_FOUND before any question
	if _, _, err = work.Get(ctx, o.env, Noun, table, id); err != nil {
		return work.WriteOut{}, err
	}
	// the one-row form asks as the engine did under Confirm Always
	// (FA-DEL-1); the CLI prompts and re-runs with yes, so the fence below
	// always runs on a confirmed delete.
	if err = bulk.Ask(ctx, "run mission delete?"); err != nil {
		return work.WriteOut{}, err
	}
	// TASK 6.12 / FA-TASK-7: the mission's claimed tasks are fenced in the
	// delete batch before the tombstones, as a task subtree delete does.
	rows, err := o.missionSourced(ctx, id.String())
	if err != nil {
		return work.WriteOut{}, err
	}
	bctx, _, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return work.WriteOut{}, err
	}
	n, err := opstask.FenceClaimed(bctx, o.env, rows)
	if err != nil {
		return work.WriteOut{}, err
	}
	human := trashline.Moved(id.String(), id.String())
	if n > 0 {
		human += "  fenced " + strconv.Itoa(n) + " claimed task(s): their holders' next write fails\n"
	}
	return work.Delete(bctx, o.env, Noun, table, id, human)
}

// missionSourced are the live task rows of mission id with their stores.
func (o ops) missionSourced(ctx context.Context, id string) ([]work.Sourced, error) {
	all, err := work.ListAll(ctx, o.env, schema.TableTasks, "")
	if err != nil {
		return nil, err
	}
	var out []work.Sourced
	for _, s := range all {
		if !s.Row.Deleted && work.Field(s.Row, work.FieldMission) == id {
			out = append(out, s)
		}
	}
	return out, nil
}

// search matches missions by title, body and status.
func (o ops) search(ctx context.Context, in SearchIn) (work.SearchEnvelope, error) {
	return work.SearchTable(ctx, o.env, table, KindSearch, in.Query, in.Limit, in.Project, in.Columns, in.Store, true, work.FieldTitle, work.FieldBody, work.FieldStatus)
}
