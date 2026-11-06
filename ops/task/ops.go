// Package task defines the `task` noun's operations (STORE_SERVER_SPEC 9.1,
// layer L5) on the new core: add, list, show, edit, start, done, cancel,
// delete, search and the triage / someday / deferred views. Each is an
// op.Define spec with typed input and output; the CLI commands, the REST
// routes and the generated reference are built from these specs.
//
// Flag spellings follow v0.1.17 `lore task …`. The coordination verbs
// (claim, next, heartbeat, release, revoke, reassign, note, merged) stay
// on the signals machinery of the old binary (TSQ / p4 brief); done and
// cancel here are the unclaimed path. --live / --no-live are back on add
// and edit (PENDING-60, as v0.1.17): engine.LiveFlags, read by the engine.
package task

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/sortspec"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	rowexpand "github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names. The CLI path and REST route derive from them
// (op/route.go): task.add → `lore task add`, POST /v1/tasks.
const (
	// Noun is the CLI noun of every task operation (`lore task …`).
	Noun       = "task"
	OpAdd      = "task.add"
	OpList     = "task.list"
	OpShow     = "task.show"
	OpEdit     = "task.edit"
	OpStart    = "task.start"
	OpDone     = "task.done"
	OpCancel   = "task.cancel"
	OpDelete   = "task.delete"
	OpSearch   = "task.search"
	OpTriage   = "task.triage"
	OpSomeday  = "task.someday"
	OpDeferred = "task.deferred"
)

// Task field names (schema.TableTasks).
const (
	fieldPriority   = "priority"
	fieldCommitment = "commitment"
	fieldDeferred   = "deferred_until"
	fieldDue        = "due_at"
	fieldStarted    = "started_at"
	fieldAssigned   = "assigned_to_actor_id"
	fieldBlockedBy  = "blocked_by"
	fieldTimebox    = "timebox_seconds"
	fieldRole       = "role"
	fieldDoneBy     = "done_by"
	fieldDoneCommit = "done_commit"
	fieldDonePR     = "done_pr"
	fieldStatusStr  = "status_str"
	fieldDisplay    = "display_name"
)

// Defaults (v0.1.17 flag and schema defaults).
const (
	DefaultPriority   = schema.TaskPriorityMedium
	DefaultCommitment = schema.TaskCommitmentAccepted
	// DefaultTasklistTitle names the list a task joins when --tasklist is
	// left out (created on demand, status_str DefaultTasklistStatus).
	DefaultTasklistTitle  = "default"
	DefaultTasklistStatus = "active"
	// searchFetch is how many hits search asks the caches for before its
	// filters; --limit is applied after them.
	searchFetch = work.MaxSearchLimit
)

// Dependency modes of --after (dbent/schema DependencyUntil).
const (
	untilDone   = "done"
	untilMerged = "merged"
)

// Memory fields a --lesson writes (schema memories; v0.1.17
// addLinkedMemory: source_kind lesson, source_ref the task id).
const (
	lessonSourceKind = "lesson"
	lessonKind       = schema.MemoryKindRetrieved
	lessonTrust      = 0.5
)

// table is the tasks table.
const table row.Table = schema.TableTasks

// Actorer is the optional Env upgrade task.done uses to record done_by
// (wire.App has it). Without it done_by is left empty and the row's
// envelope updated_by names the same actor.
type Actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// Ops returns every task operation bound to env, for op.NewRegistry.
func Ops(env work.Env) []op.Entry {
	o := ops{env: env}
	return append([]op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new task",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), append([]errs.Code{errs.Unsupported, errs.ResourceAmbiguous}, treeAddErrors...)...),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a task", CLI: `lore task add "Fix the login redirect" --priority high`, Want: "tsk_"},
				{Title: "a proposed task, as JSON", CLI: `lore task add "Try a cache" --commitment proposed --json`, Want: `"task.show"`},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List tasks",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(work.ReadErrors), errs.Unsupported),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore task list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show task details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.show),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit task fields (only flags you pass are applied)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), append([]errs.Code{errs.Unsupported, errs.OpenChildren, errs.ResourceAmbiguous}, treeAddErrors...)...),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.editOp),
		op.Define(op.Spec{
			Name: OpStart, Summary: "Mark task as in_progress",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, o.start),
		op.Define(op.Spec{
			Name: OpDone, Summary: "Mark a task done (with the code that finished it)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.OpenChildren, errs.OpenItems),
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "done with a lesson", CLI: `lore task done tsk_<id> --pr 412 --lesson "invite tokens expire in 48h"`, Want: "done"}},
		}, o.done),
		op.Define(op.Spec{
			Name: OpCancel, Summary: "Cancel a task",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.OpenChildren),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.cancel),
		op.Define(op.Spec{
			Name: OpSplit, Summary: "Split a task into sub-tasks (one batch)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), treeAddErrors...),
			Examples: []op.Example{
				{Title: "preview a split", CLI: `lore task split tsk_<id> --title "Export query" --title "Download button" --dry-run`, Want: "would add"},
				// P3-TASK-1: flags before the first title are shared, after a title that sub-task's own.
				{Title: "per-child flags", CLI: `lore task split tsk_<id> --priority low "Export query" --assign @ann --priority high "Download button" --dry-run`, Want: "priority high, assign @ann"},
			},
		}, o.split),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a task and its sub-tasks (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors,
			// TSQ3: the op asks itself, so the question can list the subtree
			// and its claimed children (bulk.Ask); it always asks.
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "delete without a terminal", CLI: "lore task delete tsk_<id> --yes", Want: "moved to the trash"}},
		}, o.deleteOp),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across task title + body (BM25 ranked)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.SearchErrors,
			Examples: []op.Example{{Title: "search as JSON", CLI: `lore task search "login" --json`, Want: `"task.search"`}},
		}, o.search),
		op.Define(op.Spec{
			Name: OpTriage, Summary: "Show proposed tasks (agent-suggested, not yet committed)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.triage),
		op.Define(op.Spec{
			Name: OpSomeday, Summary: "Show someday/maybe tasks (parking lot, no commitment)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.someday),
		op.Define(op.Spec{
			Name: OpDeferred, Summary: "Show deferred tasks (snoozed; deferred_until in the future)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.deferred),
	}, slices.Concat(timeOps(o), recurOps(o), templateOps(o), checklistOps(o), watchOps(o), relateOps(o), triageOps(o), historyOps(o), exportOps(o), viewOps(o), taskViewOps(o), treeCheckOps(o))...)
}

// treeAddErrors are the refusals of placing a task in the tree (TASK 24).
// add and edit also list E_RESOURCE_AMBIGUOUS: --ref takes a resource name
// (RES 3.11, rowref.Parse) and two live resources of that name refuse.
var treeAddErrors = []errs.Code{errs.Cycle, errs.ParentClosed, errs.ParentMission, errs.ParentProject, errs.TreeDepth, errs.TreeWidth}

// ops binds the run functions to one Env. deco, set per call on the value
// copy (list), adds the tree columns to each brief.
type ops struct {
	env  work.Env
	deco func(*Brief, row.Row)
	// meta is list's metadata filter (METADATA 3.7), set per call.
	meta work.MetaFlags
	// trash is list's --include-deleted / --deleted-only (DQ10, DELETION
	// 3.2), set per call.
	trash metalist.Trash
}

// notYet refuses a v0.1.17 team flag on a surface that supplies no
// work.Team (the roster and claims live in the host, not in a row store).
func notYet(flag, hint string) error {
	return errs.New(errs.Unsupported, "task: --"+flag+" needs the team roster and claims, which this surface does not provide").WithHint(hint)
}

// taskID parses a task id argument.
func taskID(s string) (ids.AnyID, error) { return work.ParseID(Noun, ids.PrefixTask, s) }

// add creates one task in the target store (--to, else the default).
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	if in.Template != "" {
		return o.addTemplate(ctx, in) // TASK 11.3
	}
	if len(in.Args) > 0 || in.DryRun {
		return AddOut{}, errs.New(errs.InvalidInput, "task add: --arg and --dry-run go with --template")
	}
	// --assign resolves before anything is written (v0.1.17: the default
	// task list is not created for a handle on no roster).
	assign, err := o.resolveAssigns(ctx, in.Assign)
	if err != nil {
		return AddOut{}, err
	}
	if err = o.refuseClientTeam(ctx, assign.first(), store.Source(in.To)); err != nil {
		return AddOut{}, err
	}
	pe, err := o.peopleFlags(ctx, assign, in.Lead, in.AssignedTo, nil, nil, nil, nil)
	if err != nil {
		return AddOut{}, err
	}
	refs, err := o.parseRefs(ctx, in.Ref)
	if err != nil {
		return AddOut{}, err
	}
	rel, err := relationOf(in.Relation)
	if err != nil {
		return AddOut{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return AddOut{}, errs.New(errs.InvalidInput, "task add: the title is empty").WithHint(`lore task add "<title>"`)
	}
	project, err := work.Project(ctx, o.env, Noun, in.Project)
	if err != nil {
		return AddOut{}, err
	}
	// TASK 6.5: a sub-task inherits from its parent before the defaults.
	var par *addParent
	if in.Parent != "" {
		if par, err = o.addParent(ctx, &in, project.String()); err != nil {
			return AddOut{}, err
		}
	}
	priority := orDefault(in.Priority, DefaultPriority)
	if err = work.OneOf(Noun, "priority", priority, schema.TaskPriorities()); err != nil {
		return AddOut{}, err
	}
	commitment := orDefault(in.Commitment, DefaultCommitment)
	if err = work.OneOf(Noun, "commitment", commitment, schema.TaskCommitments()); err != nil {
		return AddOut{}, err
	}
	if in.Key != "" {
		if s, ok, kerr := o.byKey(ctx, project.String(), in.Mission, in.Key); kerr != nil {
			return AddOut{}, kerr
		} else if ok {
			out := AddOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: fullOf(work.LabelerOf(o.env), s.Source, s.Row), Store: string(s.Source)}
			out.Human = work.Success + " " + s.Row.ID.String() + " — " + work.Field(s.Row, work.FieldTitle) + " (already added with --key " + in.Key + ")\n"
			out.existed = true
			return out, nil
		}
	}
	fields, err := work.Texts(Noun, map[string]string{
		work.FieldTitle: title, work.FieldBody: in.Body,
		fieldPriority: priority, fieldCommitment: commitment, fieldRole: in.Role, work.FieldKey: in.Key,
	})
	if err != nil {
		return AddOut{}, err
	}
	// D2: even the first status goes through the one transition.
	if _, err = o.setStatus(ctx, "", row.Row{}, schema.TaskStatusTodo, false, fields); err != nil {
		return AddOut{}, err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return AddOut{}, err
	}
	repoID := ""
	if in.Repo != "" {
		repo, rerr := work.RepoFor(ctx, o.env, store.Source(in.To), in.Repo)
		if rerr != nil {
			return AddOut{}, rerr
		}
		repoID = repo.String()
		if fields[work.FieldRepo], err = value.RefOf(repo); err != nil {
			return AddOut{}, err
		}
	}
	dateNotes, err := o.setDates(ctx, store.Source(in.To), fields, map[string]string{fieldDeferred: in.DeferUntil, fieldDue: in.Due})
	if err != nil {
		return AddOut{}, err
	}
	if strings.TrimSpace(in.Recur) != "" {
		// TASK 10.2: a recurring task starts a series; print what lore read.
		line, rerr := o.setRecur(ctx, store.Source(in.To), fields, Recur{}, work.Time(row.Row{Fields: fields}, fieldDue), in.Recur)
		if rerr != nil {
			return AddOut{}, rerr
		}
		dateNotes = append(dateNotes, line)
	}
	if par != nil {
		if repoID == "" && par.repo != nil {
			fields[work.FieldRepo], repoID = par.repo, par.repo.String()
		}
		if fields[fieldParent], err = parentRef(par.id); err != nil {
			return AddOut{}, err
		}
		if fields[fieldRank], err = value.TextOf(par.rank); err != nil {
			return AddOut{}, err
		}
	}
	if in.Tasklist == "" {
		if in.Tasklist, err = o.defaultTasklist(ctx, in.To, project); err != nil {
			return AddOut{}, err
		}
	}
	if err = setRefs(fields, []refFlag{
		{work.FieldMission, "mission", ids.PrefixMission, in.Mission},
		{work.FieldTasklist, "tasklist", ids.PrefixTaskList, in.Tasklist},
		{work.FieldPlan, "plan", ids.PrefixPlan, in.Plan},
		{work.FieldCreatedBy, "created-by", ids.PrefixActor, in.CreatedBy},
	}); err != nil {
		return AddOut{}, err
	}
	// TSQ5/14.4: lead + assignees + watchers (creator and assignees watch
	// unless tasks.auto_watch is false).
	if pe.autoWatch, err = o.autoWatch(ctx, store.Source(in.To)); err != nil {
		return AddOut{}, err
	}
	pe.creator = o.by(ctx)
	if _, err = o.applyPeople(ctx, row.Row{}, pe, fields); err != nil {
		return AddOut{}, err
	}
	if err = o.setEstimate(ctx, fields, store.Source(in.To), in.Estimate); err != nil {
		return AddOut{}, err
	}
	for k, v := range fields {
		if v.Kind() == value.KindNull && (k == fieldAssignees || k == fieldWatchers) {
			delete(fields, k)
		}
	}
	if v, ok := fields[fieldTeam]; ok && v.Kind() == value.KindNull {
		delete(fields, fieldTeam) // a create clears nothing
	}
	if _, set := fields[fieldTeam]; !set && par != nil && par.inh.Team != "" {
		if fields[fieldTeam], err = value.TextOf(par.inh.Team); err != nil {
			return AddOut{}, err
		}
	}
	if v, ok := fields[fieldAssigned]; ok && v.Kind() == value.KindNull {
		delete(fields, fieldAssigned)
	}
	if _, set := fields[work.FieldCreatedBy]; !set {
		// v0.1.17 stamped created_by_actor_id with the current identity.
		if v := o.currentActorRef(ctx); v != nil {
			fields[work.FieldCreatedBy] = v
		}
	}
	if len(in.After) > 0 {
		deps, derr := o.parseAfter(ctx, store.Source(in.To), in.After, repoID)
		if derr != nil {
			return AddOut{}, derr
		}
		if fields[fieldBlockedBy], err = depsValue(deps); err != nil {
			return AddOut{}, err
		}
	}
	if in.Timebox > 0 {
		fields[fieldTimebox] = value.Int(int64(in.Timebox / time.Second))
	}
	if err = o.checkRefs(ctx, store.Source(in.To), refs); err != nil {
		return AddOut{}, err
	}
	if par != nil && par.reopen != nil {
		// --reopen-parent: the parent reopens in the same batch (6.7)
		bctx, _, berr := bulk.Begin(ctx, bulk.KindTask)
		if berr != nil {
			return AddOut{}, berr
		}
		ctx = bctx
		_, prow, perr := work.Get(ctx, o.env, Noun, table, par.reopen.ID)
		if perr != nil {
			return AddOut{}, perr
		}
		patch, perr := o.reopenPatch(ctx, par.reopen.Store, prow)
		if perr != nil {
			return AddOut{}, perr
		}
		if _, perr = o.hist().Update(ctx, *par.reopen, patch); perr != nil {
			return AddOut{}, perr
		}
	}
	src, w, err := work.Create(ctx, o.env, in.To, table, fields)
	if err != nil {
		return AddOut{}, err
	}
	if err := o.saveRefs(ctx, src, project.String(), w.Row.ID, rel, refs); err != nil {
		return AddOut{}, err
	}
	out := AddOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: fullOf(work.LabelerOf(o.env), src, w.Row), Store: string(src)}
	var metaNotes strings.Builder // OV10/OV56: --meta notes as meta set prints them
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
		metaNotes.WriteString(metalist.NoteLine(string(x.Code), x.Message))
	}
	label := ""
	if l := work.Label(work.LabelerOf(o.env), src); l != "" {
		label = " → " + l // v0.1.17 task add: " → " + route.Label ("[northwind/acme]")
	}
	out.Human = work.Success + " " + w.Row.ID.String() + " — " + title + " [" + priority + "]" + label + "\n" + noteLines(dateNotes)
	if par != nil {
		out.Parent, out.Inherited = par.id, par.inh
		out.Human += par.human()
	}
	out.Human += metaNotes.String()
	return out, nil
}

// Text implements op.Texter.
func (o AddOut) Text() string { return o.Human }

// orDefault returns s, or def when s is empty.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// refFlag is one id flag that sets a ref field.
type refFlag struct {
	field, flag string
	prefix      ids.Prefix
	raw         string
}

// setRefs sets each given ref flag's field.
func setRefs(fields map[string]value.Value, flags []refFlag) error {
	for _, f := range flags {
		v, err := work.RefTo(Noun, f.flag, f.prefix, f.raw)
		if err != nil {
			return err
		}
		if v != nil {
			fields[f.field] = v
		}
	}
	return nil
}

// setDates sets each given YYYY-MM-DD flag's field.
// Values are TASK 12 phrases (dates.go); notes say what each phrase
// stored.
func (o ops) setDates(ctx context.Context, target store.Source, fields map[string]value.Value, given map[string]string) ([]string, error) {
	var notes []string
	if given[fieldDeferred] == "" && given[fieldDue] == "" {
		return nil, nil
	}
	c, err := o.clock(ctx, target)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{fieldDeferred, fieldDue} {
		raw := given[name]
		if raw == "" {
			continue
		}
		flag := "due"
		if name == fieldDeferred {
			flag = "defer-until"
		}
		v, note, err := dateValue(c, flag, raw)
		if err != nil {
			return nil, err
		}
		fields[name] = v
		if note != "" {
			notes = append(notes, note)
		}
	}
	return notes, nil
}

// byKey finds the live task added with --key in the same project and
// mission (v0.1.17 findByKey).
func (o ops) byKey(ctx context.Context, project, mission, key string) (work.Sourced, bool, error) {
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return work.Sourced{}, false, err
	}
	for _, s := range all {
		if work.Field(s.Row, work.FieldKey) == key && work.Field(s.Row, work.FieldProject) == project &&
			work.Field(s.Row, work.FieldMission) == strings.TrimSpace(mission) {
			return s, true, nil
		}
	}
	return work.Sourced{}, false, nil
}

// defaultTasklist returns the project's "default" task list in the target
// store, creating it on demand (v0.1.17 defaultTasklist).
func (o ops) defaultTasklist(ctx context.Context, target string, project ids.ID[ids.Project]) (string, error) {
	src := store.Source(target)
	if src == "" {
		src = store.SourceRepo
	}
	rows, err := o.env.List(ctx, src, schema.TableTasklists)
	if err != nil {
		return "", err
	}
	slices.SortFunc(rows, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	for _, r := range rows {
		if !r.Deleted && work.Field(r, work.FieldProject) == project.String() && work.Field(r, work.FieldTitle) == DefaultTasklistTitle {
			return r.ID.String(), nil
		}
	}
	fields, err := work.Texts(Noun, map[string]string{work.FieldTitle: DefaultTasklistTitle, fieldStatusStr: DefaultTasklistStatus})
	if err != nil {
		return "", err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return "", err
	}
	w, err := o.env.Create(ctx, store.Source(target), schema.TableTasklists, fields)
	if err != nil {
		return "", err
	}
	return w.Row.ID.String(), nil
}

// dep is one --after edge (dbent/schema TaskDependency).
type dep struct {
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id,omitempty"`
	Until     string `json:"until"`
}

// parseAfter resolves --after entries: each names an existing task; the
// mode defaults to done in the same repo, merged across repos. from is the
// store of the waiting row: blocked_by is a JSON field, not a schema ref,
// so the engine's F15 check does not see it and each edge is checked here
// (TASK 6.11 leak guard, later 443): a client row cannot wait on a row
// its readers cannot see (E_LEAK_BLOCKED, nothing written).
func (o ops) parseAfter(ctx context.Context, from store.Source, entries []string, repoID string) ([]dep, error) {
	var out []dep
	seen := map[string]bool{}
	for _, raw := range entries {
		for _, e := range strings.Split(raw, ",") {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			until := ""
			for _, mode := range []string{untilDone, untilMerged} {
				if s, ok := strings.CutSuffix(e, ":"+mode); ok {
					e, until = s, mode
				}
			}
			id, err := taskID(e)
			if err != nil {
				return nil, err
			}
			_, up, err := work.Get(ctx, o.env, Noun, table, id)
			if errs.Has(err, errs.NotFound) {
				return nil, errs.Wrap(errs.NotFound, "--after "+e+": no such task", err)
			}
			if err != nil {
				// E_AMBIGUOUS_ID, a store that cannot be read and the like
				// pass through unchanged, hint included: only a missing
				// task is "no such task" (rewriting every error hid an
				// ambiguous id).
				return nil, err
			}
			if err = pointer.Check(ctx, o.env, from, engine.Ref{Table: table, ID: id}); err != nil {
				return nil, err // names the target and carries the F15 hint
			}
			if until == "" {
				until = untilDone
				if work.Field(up, work.FieldRepo) != repoID {
					until = untilMerged
				}
			}
			if seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, dep{TaskID: e, Until: until})
		}
	}
	return out, nil
}

// depsValue is the blocked_by JSON of deps (Null when empty: cleared).
func depsValue(deps []dep) (value.Value, error) {
	if len(deps) == 0 {
		return value.Null{}, nil
	}
	raw, err := json.Marshal(deps)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task: encode --after", err)
	}
	return value.JSONOf(raw)
}

// depsOf is a row's blocked_by edges (nil when absent or unreadable).
// A row the engine just built holds the array as value.JSON, one decoded
// from a row file as a generic list: both are read through their JSON
// form (a type assertion on value.JSON alone saw no edges in any stored
// row, so checkNoCycle never found a cycle).
func depsOf(r row.Row) []dep {
	v := r.Fields[fieldBlockedBy]
	if v == nil || v.Kind() == value.KindNull {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	var out []dep
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// checkNoCycle refuses edges that would close a dependency cycle
// (E_CYCLE): id waits on deps, so no dep may (transitively) wait on id.
func (o ops) checkNoCycle(ctx context.Context, id string, deps []dep) error {
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return err
	}
	after := map[string][]string{}
	for _, s := range all {
		for _, d := range depsOf(s.Row) {
			after[s.Row.ID.String()] = append(after[s.Row.ID.String()], d.TaskID)
		}
	}
	for _, d := range deps {
		after[id] = append(after[id], d.TaskID)
	}
	// Walk from each new dep; reaching id is a cycle.
	for _, d := range deps {
		path := []string{id, d.TaskID}
		if cyc := walk(after, d.TaskID, id, map[string]bool{}, path); cyc != nil {
			return errs.New(errs.Cycle, "dependency cycle: "+strings.Join(cyc, " → ")).
				WithHint("drop one --after edge; a task may not wait on itself, even through others")
		}
	}
	return nil
}

// walk is a depth-first search from at for target; it returns the path
// when found.
func walk(after map[string][]string, at, target string, seen map[string]bool, path []string) []string {
	if at == target {
		return path
	}
	if seen[at] {
		return nil
	}
	seen[at] = true
	for _, next := range after[at] {
		if cyc := walk(after, next, target, seen, append(slices.Clone(path), next)); cyc != nil {
			return cyc
		}
	}
	return nil
}

// active is v0.1.17's ActiveTask predicate: open, accepted, not deferred.
func active(r row.Row, now time.Time) bool {
	return open(r) && work.Field(r, fieldCommitment) == schema.TaskCommitmentAccepted && !deferred(r, now)
}

// open reports a task not done or cancelled.
func open(r row.Row) bool {
	s := work.Field(r, work.FieldStatus)
	return s != schema.TaskStatusDone && s != schema.TaskStatusCancelled
}

// deferred reports a task snoozed past now.
func deferred(r row.Row, now time.Time) bool {
	t := work.Time(r, fieldDeferred)
	return !t.IsZero() && t.After(now)
}

// list returns the tasks of every attached store passing v0.1.17's
// filters (default: open, accepted, not deferred).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	// TASK 16.2: a saved view folds into the flags first.
	in, _, err := o.applyView(ctx, in)
	if err != nil {
		return ListOut{}, err
	}
	team, err := o.teamList(ctx, in)
	if err != nil {
		return ListOut{}, err
	}
	if in.Status != "" {
		// FLAG2: an unknown --status is an invalid argument (exit 20), not an
		// empty list.
		if err = work.OneOf(Noun, "status", in.Status, schema.TaskStatuses()); err != nil {
			return ListOut{}, err
		}
	}
	if in.Commitment != "" {
		if err = work.OneOf(Noun, "commitment", in.Commitment, schema.TaskCommitments()); err != nil {
			return ListOut{}, err
		}
	}
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	if in.Assignee != "" {
		if in.Assignee, err = o.actorRef(ctx, in.Assignee); err != nil {
			return ListOut{}, err
		}
	}
	if in.Lead != "" {
		if in.Lead, err = o.actorRef(ctx, in.Lead); err != nil {
			return ListOut{}, err
		}
	}
	unit, err := o.estimateUnit(ctx, "")
	if err != nil {
		return ListOut{}, err
	}
	allowed := []string{schema.TaskCommitmentAccepted}
	if in.IncludeProposed {
		allowed = append(allowed, schema.TaskCommitmentProposed)
	}
	if in.IncludeSomeday {
		allowed = append(allowed, schema.TaskCommitmentSomeday)
	}
	// XQ7: --where is the one filter grammar; its terms AND with the flags.
	keys := taskKeys()
	m, whereMeta, err := o.whereMatcher(ctx, in.Where, keys)
	if err != nil {
		return ListOut{}, err
	}
	// METADATA 3.7: a meta.<key> --sort goes to the metadata filter and
	// replaces the field sort; --meta / --place narrow the rows; --where
	// meta.<key> terms join --meta (r3-grammar).
	fieldSort := in.Sort
	o.meta = work.MetaFlags{Meta: append(slices.Clone(in.Meta), whereMeta...), Place: in.Place}
	for _, s := range in.Sort {
		if strings.HasPrefix(s, "meta.") {
			if o.meta.Sort != "" {
				return ListOut{}, errs.New(errs.InvalidInput, "task list: give one --sort meta.<key>")
			}
			o.meta.Sort, fieldSort = s, nil
		}
	}
	// TASK 13 (OV15 leftover): --sort urgency[:asc|:desc] comes first and
	// ranks by the computed score; the other keys break its ties.
	urg, fieldSort, err := urgencySort(fieldSort)
	if err != nil {
		return ListOut{}, err
	}
	// TIME 10: time_spent / elapsed sort keys and the time filters.
	tq, fieldSort, err := o.timeQueryOf(ctx, in, fieldSort)
	if err != nil {
		return ListOut{}, err
	}
	timeOf := o.timeIndex(ctx, tq)
	o.trash = in.Trash
	// XQ10: --sort key[:asc|:desc]; ties keep the default order (stable).
	specs, err := listSort(fieldSort, keys)
	if err != nil {
		return ListOut{}, err
	}
	get, sla, err := o.withSLA(ctx, rowGetter(keys))
	if err != nil {
		return ListOut{}, err
	}
	now := opclock.Now(o.env)
	// TASK 6.3/6.6: tree filters and the derived rollup of umbrellas.
	f, err := o.forest(ctx, project)
	if err != nil {
		return ListOut{}, err
	}
	var order func(a, b row.Row) int
	if len(specs) > 0 {
		order = func(a, b row.Row) int { return sortspec.Compare(specs, sortValue(keys, a), sortValue(keys, b)) }
	}
	if len(tq.sort) > 0 {
		order = tq.timeOrder(timeOf, order)
	}
	if urg != 0 {
		if order, err = o.urgencyOrder(ctx, f, now, urg, order); err != nil {
			return ListOut{}, err
		}
	}
	inTree, err := treeFilter(f, in)
	if err != nil {
		return ListOut{}, err
	}
	o.deco = func(b *Brief, r row.Row) {
		decorate(f, b, r)
		e := estimateInfo(f, r, unit)
		b.Estimate, b.EstimateUnit, b.EstimateSubtree, b.Unestimated = e.Estimate, e.Unit, e.Subtree, e.Unestimated
		b.Lead = work.Field(r, fieldAssigned)
		b.SLA = sla(r)
		if tq.active() {
			tq.timeColumns(b, timeOf(r))
		}
		cellsOf(tq.columns, get(r), b) // TASK 16.2 column table
	}
	out, err := o.viewWith(ctx, in.Store, project, team.state, order, func(r row.Row) bool {
		status, commit := work.Field(r, work.FieldStatus), work.Field(r, fieldCommitment)
		switch {
		case !inTree(r.ID.String()):
			return false
		case !m.Match(get(r)),
			in.Commitment != "" && commit != in.Commitment,
			in.Status != "" && status != in.Status,
			in.Mission != "" && work.Field(r, work.FieldMission) != in.Mission,
			in.Assignee != "" && !assignedTo(r, in.Assignee),
			in.Lead != "" && !leadIs(r, in.Lead),
			in.Series != "" && !inSeries(r, in.Series),
			team.keep != nil && !team.keep(r),
			tq.active() && !tq.keepTime(timeOf(r)):
			return false
		case in.All:
			return true
		case in.Status == "" && !open(r),
			in.Commitment == "" && !slices.Contains(allowed, commit),
			!in.IncludeDeferred && deferred(r, now):
			return false
		}
		return true
	})
	out.Columns = tq.columns
	return out, err
}

// view lists the tasks keep accepts, in v0.1.17's envelope.
func (o ops) view(ctx context.Context, only, project string, keep func(row.Row) bool) (ListOut, error) {
	return o.viewWith(ctx, only, project, nil, nil, keep)
}

// viewWith is view whose human lines carry state(row) when it says
// something (task list's STATE column; nil = none), in order when set
// (task list --sort; nil keeps the stores' order).
func (o ops) viewWith(ctx context.Context, only, project string, state func(row.Row) string, order func(a, b row.Row) int, keep func(row.Row) bool) (ListOut, error) {
	all, err := work.ListAll(ctx, o.env, table, only)
	if err != nil {
		return ListOut{}, err
	}
	// DQ10: --include-deleted adds the tasks in the trash; --deleted-only
	// lists only them (DELETION 3.2).
	trashed := map[string]bool{}
	if o.trash.Any() {
		del, derr := o.trashedTasks(ctx, only)
		if derr != nil {
			return ListOut{}, derr
		}
		for _, d := range del {
			trashed[d.Row.ID.String()] = true
		}
		if o.trash.DeletedOnly {
			all = nil
		}
		all = append(all, del...)
	}
	if order != nil {
		slices.SortStableFunc(all, func(a, b work.Sourced) int { return order(a.Row, b.Row) })
	}
	// METADATA 3.7: list's --meta / --place / --sort meta.<key>.
	all, notes, err := work.Narrow(ctx, o.env, all, o.meta)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: work.SchemaVersion, Kind: KindList, Data: []Brief{}, Notes: notes}
	for _, s := range all {
		if (project != "" && work.Field(s.Row, work.FieldProject) != project) || !keep(s.Row) {
			continue
		}
		b := BriefOf(work.LabelerOf(o.env), s.Source, s.Row)
		if o.deco != nil {
			o.deco(&b, s.Row)
		}
		if state != nil {
			b = withState(b, state(s.Row))
		}
		if trashed[s.Row.ID.String()] {
			b.Trashed, b.Line = true, b.Line+trashMark
		}
		out.Data = append(out.Data, b)
	}
	out.Count = len(out.Data)
	return out, nil
}

// triage lists open proposed tasks.
func (o ops) triage(ctx context.Context, in ViewIn) (ListOut, error) {
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	return o.view(ctx, "", project, func(r row.Row) bool {
		return work.Field(r, fieldCommitment) == schema.TaskCommitmentProposed && open(r)
	})
}

// someday lists someday tasks.
func (o ops) someday(ctx context.Context, in ViewIn) (ListOut, error) {
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	return o.view(ctx, "", project, func(r row.Row) bool {
		return work.Field(r, fieldCommitment) == schema.TaskCommitmentSomeday
	})
}

// deferred lists tasks snoozed past now.
func (o ops) deferred(ctx context.Context, in ViewIn) (ListOut, error) {
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	now := opclock.Now(o.env)
	return o.view(ctx, "", project, func(r row.Row) bool { return deferred(r, now) })
}

// show returns one task, from whichever attached store holds it.
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	var src store.Source
	var r row.Row
	if in.From == "" {
		src, r, err = work.Get(ctx, o.env, Noun, table, id)
	} else {
		src, r, err = o.getFrom(ctx, in.From, id)
	}
	if err != nil {
		return ShowOut{}, err
	}
	full := fullOf(work.LabelerOf(o.env), src, r)
	if err = o.showTree(ctx, &full, r, in.Tree); err != nil {
		return ShowOut{}, err
	}
	full.TimeEntries = TimeEntries(r)
	full.TimeLine = TimeLine(full.TimeEntries)
	if err = o.showMore(ctx, &full, src, r); err != nil {
		return ShowOut{}, err
	}
	if by := work.Field(r, fieldDoneBy); by != "" {
		full.DoneBy = o.actorName(ctx, by)
	}
	if full.Refs, err = o.refsOf(ctx, id); err != nil {
		return ShowOut{}, err
	}
	if full.Notes, err = notesOn(ctx, o.env, id.String()); err != nil {
		return ShowOut{}, err
	}
	if in.Why {
		if full.Why, err = o.why(ctx, r); err != nil {
			return ShowOut{}, err
		}
	}
	if in.Who {
		if full.Who, err = o.who(ctx, r); err != nil {
			return ShowOut{}, err
		}
	}
	if in.Relations {
		if full.Relations, err = o.relationsOf(ctx, r); err != nil {
			return ShowOut{}, err
		}
	}
	if t, ok := work.TeamFrom(ctx); ok && src != store.SourceRepo {
		if d, ok := t.(ShowDetailer); ok {
			detail, has, err := d.ShowDetail(ctx, id.String())
			if err != nil {
				return ShowOut{}, err
			}
			if has {
				full.Detail = detail
			}
		}
	}
	if in.Signals { // F7 Debug: the claim as seen now, after the body
		text, err := claimShow(ctx, id.String())
		if err != nil {
			return ShowOut{}, err
		}
		full.Detail.Tail += text
	}
	out := ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: full}
	if in.Expand { // SECRETS 2.7, 12: the row as read (--from too), stored text unchanged
		ex, _ := o.env.(rowexpand.Env)
		blk, xerr := rowexpand.Row(ctx, ex, r)
		if xerr != nil {
			return ShowOut{}, xerr
		}
		out.Expanded, out.ExpandedRefs = blk.Expanded, blk.Refs
	}
	return out, nil
}

// fromRepo is the --from value naming the repo's own store.
const fromRepo = "repo"

// getFrom reads task id from the source --from names (fixed rule 5): repo,
// an attached store's name (an org entry), or its label <org>/<folder> (the
// name a linked store is shown under: the store "northwind" attached at
// globex is northwind/globex). A source no attached store answers to is
// v0.1.17's refusal; an id the source does not hold is E_NOT_FOUND.
func (o ops) getFrom(ctx context.Context, from string, id ids.AnyID) (store.Source, row.Row, error) {
	src, ok := o.sourceNamed(from)
	if !ok {
		name, _, _ := strings.Cut(from, "/")
		return "", row.Row{}, errs.New(errs.InvalidInput, "--from "+from+": no org entry named "+name).
			WithHint("use repo, an org entry name, or <org>/<folder>")
	}
	got, r, err := o.env.Get(ctx, engine.Ref{Store: src, Table: table, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		return "", row.Row{}, errs.New(errs.NotFound, Noun+" "+work.Quote(id.String())+" not found in "+from)
	}
	return got, r, err
}

// sourceNamed maps a --from value to the attached store it names: its
// store name, or its label (Labeler); "repo" is the repo store.
func (o ops) sourceNamed(from string) (store.Source, bool) {
	if from == fromRepo {
		return store.SourceRepo, true
	}
	lab := work.LabelerOf(o.env)
	for _, s := range o.env.Stores() {
		if string(s) == from || (lab != nil && lab.StoreLabel(s) == from) {
			return s, true
		}
	}
	return "", false
}

// notesOn is v0.1.17's printTaskNotes: the task's notes (comments on it)
// from every attached store, oldest first, so a replacement agent reads
// where its predecessor stopped (LORE_TEAM_SPEC §7.5 step 5). A store's
// note carries its label ([org/folder]); internal notes point inward at a
// repo task (F15) and are shown to the team, while a client checkout has
// no such store attached and so never sees them.
func notesOn(ctx context.Context, env work.Env, taskID string) ([]string, error) {
	rows, err := work.ListAll(ctx, env, schema.TableComments, "")
	if err != nil {
		return nil, err
	}
	lab := work.LabelerOf(env)
	var on []work.Sourced
	for _, r := range rows {
		if work.Field(r.Row, schema.FieldEntityTable) == schema.TableTasks && work.Field(r.Row, schema.FieldEntityID) == taskID {
			on = append(on, r)
		}
	}
	slices.SortStableFunc(on, func(a, b work.Sourced) int { return a.Row.CreatedAt.Compare(b.Row.CreatedAt) })
	out := make([]string, 0, len(on))
	for _, r := range on {
		line := r.Row.CreatedAt.UTC().Format(noteTimeLayout) + "  " + work.Field(r.Row, work.FieldBody)
		if l := work.Label(lab, r.Source); l != "" && r.Source != store.SourceRepo {
			line += "  " + l
		}
		out = append(out, line)
	}
	return out, nil
}

// noteTimeLayout is the time of a note line (v0.1.17 printTaskNotes).
const noteTimeLayout = "2006-01-02 15:04"

// actorName is an actor's display name, else its id (v0.1.17 actorName).
func (o ops) actorName(ctx context.Context, id string) string {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return id
	}
	_, r, err := o.env.Get(ctx, engine.Ref{Table: schema.TableActors, ID: aid})
	if err != nil || r.Deleted || work.Field(r, fieldDisplay) == "" {
		// OV7: the actor row may sit where a by-id Get does not look (a
		// minimal client actor row); the list read finds it (as ownerOf).
		var ok bool
		if r, ok = o.actorRow(ctx, id); !ok || work.Field(r, fieldDisplay) == "" {
			return id
		}
	}
	return work.Field(r, fieldDisplay)
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (work.WriteOut, error) {
	assign, err := o.resolveAssigns(ctx, in.Assign)
	if err != nil {
		return work.WriteOut{}, err
	}
	pe, err := o.peopleFlags(ctx, assign, in.Lead, in.AssignedTo, in.AddAssignee, in.RemoveAssignee, in.AddWatcher, in.RemoveWatcher)
	if err != nil {
		return work.WriteOut{}, err
	}
	pe.clear = in.ClearAssignee
	id, err := taskID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	curSrc, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	if err = o.refuseClientTeam(ctx, assign.first(), curSrc); err != nil {
		return work.WriteOut{}, err
	}
	refs, err := o.parseRefs(ctx, in.Ref)
	if err != nil {
		return work.WriteOut{}, err
	}
	rel, err := relationOf(in.Relation)
	if err != nil {
		return work.WriteOut{}, err
	}
	if err = o.checkRefs(ctx, curSrc, refs); err != nil {
		return work.WriteOut{}, err
	}
	if err = o.saveRefs(ctx, curSrc, work.Field(cur, work.FieldProject), id, rel, refs); err != nil {
		return work.WriteOut{}, err
	}
	for _, c := range []struct{ flag, v string }{{"priority", in.Priority}, {"status", in.Status}, {"commitment", in.Commitment}} {
		if c.v == "" {
			continue
		}
		set := map[string][]string{"priority": schema.TaskPriorities(), "status": schema.TaskStatuses(), "commitment": schema.TaskCommitments()}[c.flag]
		if err = work.OneOf(Noun, c.flag, c.v, set); err != nil {
			return work.WriteOut{}, err
		}
	}
	patch, err := work.Texts(Noun, map[string]string{
		work.FieldTitle: strings.TrimSpace(in.Title), work.FieldBody: in.Body, fieldPriority: in.Priority,
		fieldCommitment: in.Commitment, fieldRole: in.Role,
	})
	if err != nil {
		return work.WriteOut{}, err
	}
	timeHint := ""
	if in.Status != "" {
		// D2: --status goes through the one transition (time spans).
		if timeHint, err = o.setStatus(ctx, curSrc, cur, in.Status, false, patch); err != nil {
			return work.WriteOut{}, err
		}
	}
	dateNotes, err := o.setDates(ctx, curSrc, patch, map[string]string{fieldDeferred: in.DeferUntil, fieldDue: in.Due})
	if err != nil {
		return work.WriteOut{}, err
	}
	if strings.TrimSpace(in.Recur) != "" {
		// TASK 10.3: the rule changes for this and later instances.
		line, rerr := o.editRecur(ctx, curSrc, cur, patch, in.Recur)
		if rerr != nil {
			return work.WriteOut{}, rerr
		}
		dateNotes = append(dateNotes, line)
	}
	if err = setRefs(patch, []refFlag{
		{work.FieldMission, "mission", ids.PrefixMission, in.Mission},
		{work.FieldTasklist, "tasklist", ids.PrefixTaskList, in.Tasklist},
		{work.FieldPlan, "plan", ids.PrefixPlan, in.Plan},
	}); err != nil {
		return work.WriteOut{}, err
	}
	if pe.autoWatch, err = o.autoWatch(ctx, curSrc); err != nil {
		return work.WriteOut{}, err
	}
	peopleLine, err := o.applyPeople(ctx, cur, pe, patch)
	if err != nil {
		return work.WriteOut{}, err
	}
	if peopleLine != "" {
		timeHint += "  " + peopleLine + "\n"
	}
	if err = o.setEstimate(ctx, patch, curSrc, in.Estimate); err != nil {
		return work.WriteOut{}, err
	}
	clears := []struct {
		on    bool
		field string
	}{
		{in.ClearDefer, fieldDeferred}, {in.ClearDue, fieldDue}, {in.ClearMission, work.FieldMission},
		{in.ClearPlan, work.FieldPlan}, {in.ClearEstimate, fieldEstimate},
	}
	for _, c := range clears {
		if c.on {
			patch[c.field] = value.Null{}
		}
	}
	if len(in.After) > 0 {
		deps, derr := o.parseAfter(ctx, curSrc, in.After, work.Field(cur, work.FieldRepo))
		if derr != nil {
			return work.WriteOut{}, derr
		}
		if err = o.checkNoCycle(ctx, id.String(), deps); err != nil {
			return work.WriteOut{}, err
		}
		if patch[fieldBlockedBy], err = depsValue(deps); err != nil {
			return work.WriteOut{}, err
		}
	}
	if in.Timebox > 0 {
		patch[fieldTimebox] = value.Int(int64(in.Timebox / time.Second))
	}
	// TASK 6.3/6.8: re-parent, detach, reorder; the subtree's mission and a
	// reopened parent are written in one bat_ batch with the task.
	te, err := o.editTree(ctx, in, curSrc, cur)
	if err != nil {
		return work.WriteOut{}, err
	}
	maps.Copy(patch, te.patch)
	if len(te.others) > 0 {
		bctx, _, berr := bulk.Begin(ctx, bulk.KindTask)
		if berr != nil {
			return work.WriteOut{}, berr
		}
		ctx = bctx
		for _, u := range te.others {
			if _, uerr := o.hist().Update(ctx, u.ref, u.patch); uerr != nil {
				return work.WriteOut{}, uerr
			}
		}
	}
	if len(patch) == 0 && len(refs) > 0 {
		return work.WriteOut{ID: id.String(), Store: work.SourceName(curSrc), Human: work.Success + " " + id.String() + " updated\n"}, nil
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) { // OV56: --meta alone is a change
		return work.WriteOut{}, errs.New(errs.InvalidInput, "task edit: nothing to change").
			WithHint("give at least one flag, e.g. --title, --status, --priority, --due")
	}
	out, err := work.Patch(ctx, o.hist(), Noun, table, id, patch, work.Success+" "+id.String()+" updated\n"+noteLines(dateNotes)+timeHint)
	if err != nil || in.Status != schema.TaskStatusInProgress {
		return out, err
	}
	// TASK 6.7: tasks.auto_start_parent, under the parent the edit leaves
	// the task in (FA-TASK-2).
	moved := cur
	if v, ok := te.patch[fieldParent]; ok {
		moved.Fields = maps.Clone(cur.Fields)
		moved.Fields[fieldParent] = v
	}
	line, err := o.autoStartParent(ctx, moved)
	out.Human += line
	return out, err
}

// start marks a task in_progress; starting work also commits it and
// un-defers it (v0.1.17 updateTaskStatus).
func (o ops) start(ctx context.Context, in IDIn) (work.WriteOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	src, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	patch, hint, err := o.statusPatch(ctx, src, cur, schema.TaskStatusInProgress, fieldStarted)
	if err != nil {
		return work.WriteOut{}, err
	}
	// design 4.1: started_at keeps the FIRST start; it is no longer overwritten.
	if !work.Time(cur, fieldStarted).IsZero() {
		delete(patch, fieldStarted)
	}
	out, err := work.Patch(ctx, o.hist(), Noun, table, id, patch, work.Success+" "+id.String()+" started\n"+hint)
	if err != nil {
		return out, err
	}
	// TASK 6.7: tasks.auto_start_parent (FA-TASK-2).
	line, err := o.autoStartParent(ctx, cur)
	out.Human += line
	return out, err
}

// statusPatch sets status through setStatus (D2), stamps the time field,
// accepts the task and clears its deferral; hint is setStatus's line.
func (o ops) statusPatch(ctx context.Context, src store.Source, cur row.Row, status, stamp string) (map[string]value.Value, string, error) {
	patch, err := work.Texts(Noun, map[string]string{fieldCommitment: schema.TaskCommitmentAccepted})
	if err != nil {
		return nil, "", err
	}
	hint, err := o.setStatus(ctx, src, cur, status, false, patch)
	if err != nil {
		return nil, "", err
	}
	patch[stamp] = work.Now(o.env)
	patch[fieldDeferred] = value.Null{}
	return patch, hint, nil
}

// done marks a task done with the code that finished it and saves the
// lesson (v0.1.17 plainDone + finishDone). A claimed task's fencing stays
// with the signals machinery of the old binary.
func (o ops) done(ctx context.Context, in DoneIn) (DoneOut, error) {
	if in.PR < 0 {
		return DoneOut{}, errs.New(errs.InvalidInput, "--pr must be a positive number")
	}
	id, err := taskID(in.ID)
	if err != nil {
		return DoneOut{}, err
	}
	// TASK 6.7: open sub-tasks refuse (E_OPEN_CHILDREN) unless --children
	// closes them in the same bat_ batch; close_reason completed (8.3).
	ctx, f, childLines, err := o.closeTree(ctx, id, "done", in.Children, childrenDone)
	if err != nil {
		return DoneOut{}, err
	}
	// F7: a claimed task's done ends the claim first (closeheld.go).
	if hc, ok := heldCloser(ctx); ok {
		if in.Commit, err = hc.CloseHeldDone(ctx, id.String(), in.Commit, in.PR); err != nil {
			return DoneOut{}, err
		}
	}
	// --from names the source of the id (fixed rule 5): another org
	// entry's store the runner attached for this run (op.SourceNamer).
	dsrc, dcur, err := o.getSource(ctx, in.From, id)
	if err != nil {
		return DoneOut{}, err
	}
	patch, err := o.closePatch(ctx, dsrc, dcur, schema.TaskStatusDone, "")
	if err != nil {
		return DoneOut{}, err
	}
	// TSQ14: unticked checklist items refuse unless --tick-all / --force.
	forced, err := o.openItemsGuard(ctx, dcur, in, patch)
	if err != nil {
		return DoneOut{}, err
	}
	if in.Commit != "" {
		if patch[fieldDoneCommit], err = value.TextOf(strings.TrimSpace(in.Commit)); err != nil {
			return DoneOut{}, err
		}
	}
	if in.PR > 0 {
		patch[fieldDonePR] = value.Int(int64(in.PR))
	}
	if a, ok := o.env.(Actorer); ok {
		if actor, aerr := a.Actor(ctx); aerr == nil && !actor.IsZero() {
			if patch[fieldDoneBy], err = value.TextOf(actor.String()); err != nil {
				return DoneOut{}, err
			}
		}
	}
	code := ""
	switch {
	case in.PR > 0:
		code = " (pr #" + strconv.Itoa(in.PR) + "); waiting for merge"
	case in.Commit != "":
		code = " (commit " + shortSHA(in.Commit) + "); waiting for merge"
	}
	// TSQ14 / 17.2: the history's status entry records the unticked items.
	w, err := o.patchAt(withHistoryNote(ctx, "open_items_at_close: "+strconv.Itoa(forced)), o.hist(), dsrc, id, patch, work.Success+" "+id.String()+" done"+code+"\n")
	if err != nil {
		return DoneOut{}, err
	}
	out := DoneOut{ID: w.ID, Store: w.Store, Human: w.Human + childLines, OpenItemsAtClose: forced}
	if forced > 0 {
		out.Human += "  closed with " + strconv.Itoa(forced) + " unticked checklist items (--force)\n"
	}
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning(x))
	}
	parentLine := ""
	if out.Parent, out.Next, parentLine, err = o.afterClose(ctx, f, id.String()); err != nil {
		return DoneOut{}, err
	}
	out.Human += parentLine
	// TASK 10.4: a recurring instance creates the next one.
	nextLine := ""
	if out.Recur, nextLine, err = o.spawnNext(ctx, f, dsrc, dcur, opclock.Now(o.env)); err != nil {
		return DoneOut{}, err
	}
	out.Human += nextLine
	switch {
	case strings.TrimSpace(in.Lesson) != "":
		mem, lerr := o.lesson(ctx, store.Source(w.Store), id, in.Lesson)
		if lerr != nil {
			return DoneOut{}, lerr
		}
		out.Lesson = mem
		out.Human += "  " + lessonSourceKind + " → " + mem + "\n"
	case !in.NoLesson:
		out.Human += "  learned something? `lore memory add --body \"…\"` (or pass --lesson next time)\n"
	}
	return out, nil
}

// Text implements op.Texter.
func (o DoneOut) Text() string { return o.Human }

// shortSHAlen is how much of a commit v0.1.17 printed.
const shortSHAlen = 7

// shortSHA is the first 7 characters of a commit.
func shortSHA(s string) string {
	if len(s) > shortSHAlen {
		return s[:shortSHAlen]
	}
	return s
}

// lesson saves a memory pointing at the task, in the store that holds the
// task (v0.1.17 D8 routed it there).
func (o ops) lesson(ctx context.Context, src store.Source, task ids.AnyID, text string) (string, error) {
	project, err := o.env.Project(ctx)
	if err != nil {
		return "", err
	}
	_, r, err := work.Get(ctx, o.env, Noun, table, task)
	if err == nil && work.Field(r, work.FieldProject) != "" {
		if p, perr := ids.Parse[ids.Project](work.Field(r, work.FieldProject)); perr == nil {
			project = p
		}
	}
	fields, err := work.Texts(Noun, map[string]string{
		"body": strings.TrimSpace(text), "kind": lessonKind, "source_kind": lessonSourceKind, "source_ref": task.String(),
	})
	if err != nil {
		return "", err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return "", err
	}
	trust, err := value.Float(lessonTrust)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "task done: trust score", err)
	}
	fields["trust_score"] = trust
	fields["tx_at"] = work.Now(o.env)
	w, err := o.env.Create(ctx, src, schema.TableMemories, fields)
	if err != nil {
		return "", err
	}
	return w.Row.ID.String(), nil
}

// cancel marks a task cancelled with its close reason (8.3); open
// sub-tasks follow the done rule (6.7, default suggestion --children
// cancel).
func (o ops) cancel(ctx context.Context, in CancelIn) (DoneOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return DoneOut{}, err
	}
	// F7: cancelling a claimed task is a human override through the host's
	// fenced writer (closeheld.go); it then prints the host's line.
	if hc, ok := heldCloser(ctx); ok {
		line, handled, herr := hc.CloseHeldCancel(ctx, id.String())
		if herr != nil {
			return DoneOut{}, herr
		}
		if handled {
			return DoneOut{ID: id.String(), Human: line + "\n"}, nil
		}
	}
	csrc, ccur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return DoneOut{}, err
	}
	patch, err := o.closePatch(ctx, csrc, ccur, schema.TaskStatusCancelled, in.Reason)
	if err != nil {
		return DoneOut{}, err
	}
	ctx, f, childLines, err := o.closeTree(ctx, id, "cancel", in.Children, childrenCancel)
	if err != nil {
		return DoneOut{}, err
	}
	w, err := work.Patch(ctx, o.hist(), Noun, table, id, patch, work.Success+" "+id.String()+" cancelled\n")
	if err != nil {
		return DoneOut{}, err
	}
	out := DoneOut{ID: w.ID, Store: w.Store, Human: w.Human + childLines}
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning(x))
	}
	parentLine := ""
	if out.Parent, out.Next, parentLine, err = o.afterClose(ctx, f, id.String()); err != nil {
		return DoneOut{}, err
	}
	out.Human += parentLine
	if _, recurs := RecurOf(ccur); recurs {
		out.Human += "  no next instance: cancel ends the series (`lore task recur skip` skips one)\n"
	}
	return out, nil
}

// delete tombstones the task and, through the parent relation's cascade,
// its whole subtree, under one bat_ batch of kind delete. It always asks
// (--yes); with at least one sub-task the question lists the count, the
// first titles and every claimed child with its holder (TSQ3, DQ5).
func (o ops) delete(ctx context.Context, in DeleteIn) (work.WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	id, err := taskID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	f, err := o.forest(ctx, work.Field(cur, work.FieldProject))
	if err != nil {
		return work.WriteOut{}, err
	}
	desc := f.tree.Descendants(id.String())
	question := "delete task " + id.String() + " " + work.Field(cur, work.FieldTitle) + "?"
	var claimed []string
	if len(desc) > 0 {
		if question, claimed, err = o.subtreeQuestion(ctx, f, id.String()); err != nil {
			return work.WriteOut{}, err
		}
	}
	// FB-TASK-1: a claimed task itself is fenced too, so a restore never
	// hands the old holder its claim back (its next write fails; it
	// releases, which drops the claim, and claims again).
	selfClaimed, err := selfClaimNow(ctx, id.String())
	if err != nil {
		return work.WriteOut{}, err
	}
	if selfClaimed {
		question += " (it is claimed: the holder's next write fails)"
	}
	if err = bulk.Ask(ctx, question); err != nil {
		return work.WriteOut{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return work.WriteOut{}, err
	}
	fence := claimed
	if selfClaimed {
		fence = append([]string{id.String()}, claimed...)
	}
	// 6.12: claimed children are fenced before the tombstones (FA-TASK-3).
	if err = o.fenceClaimed(bctx, f, fence); err != nil {
		return work.WriteOut{}, err
	}
	human := trashline.Moved(id.String(), id.String())
	if len(desc) > 0 {
		human = trashline.MovedBatch(strconv.Itoa(len(desc)+1)+" tasks ("+id.String()+" and its sub-tasks)", batch.String())
	}
	if len(claimed) > 0 {
		human += "  fenced " + strconv.Itoa(len(claimed)) + " claimed sub-task(s): their holders' next write fails\n"
	}
	if selfClaimed {
		human += "  fenced the claim on " + id.String() + ": its holder's next write fails; `lore task release` drops it\n"
	}
	return work.Delete(bctx, o.env, Noun, table, id, human)
}

// search matches the query against every attached store's cache, keeps
// active tasks (unless --all) in scope, and cuts at --limit after the
// filters.
func (o ops) search(ctx context.Context, in SearchIn) (work.SearchEnvelope, error) {
	limit, err := work.Limit(in.Limit)
	if err != nil {
		return work.SearchEnvelope{}, err
	}
	repo := ""
	if in.Repo != "" {
		r, rerr := o.env.RepoID(ctx, in.Repo)
		switch {
		case rerr == nil:
			repo = r.String()
		case errs.Has(rerr, errs.NotFound):
			// the repo may exist only in another store's project: hits
			// there are matched by name below (repoScope), never created;
			// a name no attached store has stays v0.1.17's refusal
			found, ferr := o.repoNamedAnywhere(ctx, strings.TrimSpace(in.Repo))
			if ferr != nil {
				return work.SearchEnvelope{}, ferr
			}
			if !found {
				return work.SearchEnvelope{}, rerr
			}
			repo = repoByName + in.Repo
		default:
			return work.SearchEnvelope{}, rerr
		}
	}
	scope := repoScope{env: o.env, name: strings.TrimSpace(in.Repo), want: repo, seen: map[string]string{}}
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return work.SearchEnvelope{}, err
	}
	hits, err := work.SearchColumns(ctx, o.env, Noun, table, in.Query, in.Columns, searchFetch)
	if err != nil {
		return work.SearchEnvelope{}, err
	}
	now := opclock.Now(o.env)
	out := work.SearchEnvelope{SchemaVersion: work.SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, h := range hits {
		if len(out.Data) == limit {
			break
		}
		r := h.Row
		if (!in.All && !active(r, now)) || !work.InScope(scope.of(ctx, h.Source, work.Field(r, work.FieldRepo)), repo, in.NoInherit, in.MasterOnly, in.AllRepos) ||
			(project != "" && work.Field(r, work.FieldProject) != project) {
			continue
		}
		hit := work.HitOf(work.LabelerOf(o.env), h, true, work.FieldTitle, work.FieldBody, work.FieldStatus, fieldPriority)
		hit.Row[fieldDue] = work.TimeText(r, fieldDue)
		hit.Relations = work.Relations{
			Tasklist: op.SomeIf(work.BriefOf(ctx, o.env, schema.TableTasklists, work.Field(r, work.FieldTasklist), false)),
			Mission:  op.SomeIf(work.BriefOf(ctx, o.env, schema.TableMissions, work.Field(r, work.FieldMission), true)),
			Plan:     op.SomeIf(work.BriefOf(ctx, o.env, schema.TablePlans, work.Field(r, work.FieldPlan), false)),
		}
		out.Data = append(out.Data, hit)
	}
	out.Count = len(out.Data)
	return out, nil
}

// actorMe is the --assignee spelling of the current identity.
const actorMe = "@me"

// Actor columns the --assignee lookup reads.
const (
	fieldStableKey = "stable_key"
	// stableKeySep separates an actor kind from its name in a stable key
	// (agent:review-bot), so the bare name matches too.
	stableKeySep = ":"
)

// currentActorRef is a ref to the current identity's actor row, or nil
// when the Env cannot tell or the row does not exist (the engine refuses a
// dangling actor ref; the envelope's created_by still names the writer).
func (o ops) currentActorRef(ctx context.Context) value.Value {
	a, ok := o.env.(Actorer)
	if !ok {
		return nil
	}
	actor, err := a.Actor(ctx)
	if err != nil || actor.IsZero() {
		return nil
	}
	if _, r, gerr := o.env.Get(ctx, engine.Ref{Table: schema.TableActors, ID: actor.Any()}); gerr != nil || r.Deleted {
		return nil
	}
	v, err := value.RefOf(actor.Any())
	if err != nil {
		return nil
	}
	return v
}

// actorRef resolves an actor reference as v0.1.17's resolveActorRef did:
// an act_ id, @me (the current identity, which must have an actor row), a
// stable_key, the name part of a stable_key, or a display name (any case).
// Blank is E_INVALID_INPUT, no match E_NOT_FOUND, several E_INVALID_INPUT
// listing the candidates.
func (o ops) actorRef(ctx context.Context, raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", errs.New(errs.InvalidInput, "actor reference is empty").
			WithHint("pass an actor id (act_*), " + actorMe + ", a stable_key or a display name")
	}
	all, err := work.ListAll(ctx, o.env, schema.TableActors, "")
	if err != nil {
		return "", err
	}
	if ref == actorMe {
		a, ok := o.env.(Actorer)
		if !ok {
			return "", errs.New(errs.Unsupported, "--assignee "+actorMe+": the current identity is not known here")
		}
		me, aerr := a.Actor(ctx)
		if aerr != nil {
			return "", aerr
		}
		for _, s := range all {
			if s.Row.ID.String() == me.String() {
				return me.String(), nil
			}
		}
		return "", errs.New(errs.NotFound, "current identity has no actor yet").
			WithHint("an actor row is created on its first write, e.g. `lore task add`")
	}
	var byKey, byName []string
	for _, s := range all {
		id, key := s.Row.ID.String(), work.Field(s.Row, fieldStableKey)
		_, short, _ := strings.Cut(key, stableKeySep)
		switch {
		case id == ref:
			return id, nil
		case key == ref:
			byKey = append(byKey, id)
		case strings.EqualFold(work.Field(s.Row, fieldDisplay), ref) || strings.EqualFold(short, ref):
			byName = append(byName, id)
		}
	}
	if len(byKey) == 0 && strings.Contains(ref, stableKeySep) {
		// A repo store holds actors as the F18 projection {id,
		// display_name, kind}: no stable_key to compare. The actor id is
		// derived from the stable key (v0.1.17's id), so match by that.
		if want, kerr := identity.ActorID(ref); kerr == nil {
			for _, s := range all {
				if s.Row.ID.String() == want.String() {
					byKey = append(byKey, want.String())
					break
				}
			}
		}
	}
	switch {
	case len(byKey) == 1:
		return byKey[0], nil
	case len(byName) == 1:
		return byName[0], nil
	case len(byName) > 1:
		slices.Sort(byName)
		return "", errs.New(errs.InvalidInput, "actor "+work.Quote(ref)+" is ambiguous: "+strings.Join(slices.Compact(byName), ", ")).
			WithHint("pass the actor id")
	}
	return "", errs.New(errs.NotFound, "no actor "+work.Quote(ref)).
		WithHint("pass an actor id (act_*), " + actorMe + ", a stable_key or a display name")
}

// repoByName prefixes a search's wanted repo when --repo names no repo of
// the repo store: it can never equal a row id, so only rows repoScope
// maps by name match it.
const repoByName = "name:"

// fieldMount is a repos row's mount name (schema repos.mount_name).
const fieldMount = "mount_name"

// repoScope resolves --repo per searched store, read-only: a hit in the
// repo store keeps its repo_id; a hit in another store whose repos row has
// the --repo mount name (or id) counts as the wanted repo, since each
// store's project has its own repos rows (v0.1.17 found the repo in the
// target store's project). Nothing is created by a search.
type repoScope struct {
	env  work.Env
	name string
	want string
	seen map[string]string // repos row id → what InScope compares
}

// of is the repo value InScope compares for a hit of src with repo_id got.
func (s repoScope) of(ctx context.Context, src store.Source, got string) string {
	if got == "" || s.name == "" || src == store.SourceRepo || got == s.want {
		return got
	}
	if v, ok := s.seen[got]; ok {
		return v
	}
	v := got
	if id, err := ids.ParseAny(got); err == nil {
		if _, r, gerr := s.env.Get(ctx, engine.Ref{Store: src, Table: schema.TableRepos, ID: id}); gerr == nil && !r.Deleted &&
			(work.Field(r, fieldMount) == s.name || got == s.name) {
			v = s.want
		}
	}
	s.seen[got] = v
	return v
}

// repoNamedAnywhere reports whether any attached store holds a live repos
// row with mount name (or id) name.
func (o ops) repoNamedAnywhere(ctx context.Context, name string) (bool, error) {
	for _, src := range o.env.Stores() {
		rows, err := o.env.List(ctx, src, schema.TableRepos)
		if err != nil {
			return false, err
		}
		for _, r := range rows {
			if !r.Deleted && (work.Field(r, fieldMount) == name || r.ID.String() == name) {
				return true, nil
			}
		}
	}
	return false, nil
}

// heldCloser is the host's HeldCloser, if any.
func heldCloser(ctx context.Context) (HeldCloser, bool) {
	t, ok := work.TeamFrom(ctx)
	if !ok {
		return nil, false
	}
	c, ok := t.(HeldCloser)
	return c, ok
}
