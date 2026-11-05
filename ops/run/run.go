// Package run defines the `run` noun (layer L5, TIME_TRACKING_DESIGN 2.2,
// 4, 11.9): a machine-local log of one attempt at a task by an agent or a
// person. lore does not execute runs; the caller opens one before the
// work and closes it after.
//
//	lore run start [--task --mission --goal --agent --model --kind --retry-of]
//	    └► Env.Create(runs) ─► the machine store (schema.MachineLocal, P3-LOCAL-1)
//	lore run end <run> --outcome <o> [--summary --error --tokens-in --tokens-out --cost]
//	lore run cancel <run> [--reason]
//	    └► Env.Update(runs: status, completed_at, notes)
//	    └► the run names a task: its span goes on the task's time_entries
//	       (kind agent, source run, TIME 4.3) ─► the task's store is
//	       unreachable: the span waits in local state's time_outbox and is
//	       written on the next run end/cancel or `lore sync` (FlushOutbox, 11.9)
//
// `run step|show|replay|list` and the run_steps table are not in the 0.1.18
// design and are dropped (p3/dropped.md, PASS3 DESIGN-ONLY).
package run

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// Operation names (`lore run …`, REST /v1/runs).
const (
	Noun     = "run"
	OpStart  = "run.start"
	OpEnd    = "run.end"
	OpCancel = "run.cancel"
)

// Run kinds v0.1.17 defaulted to: "task" when --task is given, else "general".
const (
	kindTask    = "task"
	kindGeneral = "general"
)

// Human output pieces (TIME_TRACKING 2.2: "run_…0091 started",
// "run_…0091 ended  (45m 10s)").
const (
	startUnknown = "start time unknown"
	noteMark     = "! "
)

// Env is what the run operations need; *wire.App implements it. Create,
// Update and Get are engine.Engine's: the engine sends every runs row to
// the machine store whatever target is named (P3-LOCAL-1).
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// Actorer is the optional Env upgrade naming the current identity: a run's
// actor_id and the worker of its time span. Without it a run has no actor
// and its span cannot be recorded (timeentry needs an actor).
type Actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// Projecter is the optional Env upgrade naming this repo's project: the
// machine store keeps every repo's runs, so each carries its project
// (P3-LOCAL-2). Without it project_id stays empty.
type Projecter interface {
	Project(ctx context.Context) (ids.ID[ids.Project], error)
}

// LocalStater is the optional Env upgrade opening this machine's local
// state (the time_outbox, TIME 11.9). Without it an unreachable task's
// span cannot be queued and the end reports it instead.
type LocalStater interface {
	LocalState(ctx context.Context) (localstate.Store, error)
}

// Notes is the JSON kept in a run's notes field, v0.1.17's shape (runNotes
// in run_cmds.go), so a copied lore.db row reads unchanged.
type Notes struct {
	TaskID       string  `json:"task_id,omitempty"`
	MissionID    string  `json:"mission_id,omitempty"`
	AgentKind    string  `json:"agent_kind,omitempty"`
	ModelID      string  `json:"model_id,omitempty"`
	RetryOfRunID string  `json:"retry_of_run_id,omitempty"`
	Goal         string  `json:"goal,omitempty"`
	Outcome      string  `json:"outcome,omitempty"`
	Summary      string  `json:"summary,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	TokensIn     int     `json:"tokens_in,omitempty"`
	TokensOut    int     `json:"tokens_out,omitempty"`
	Cost         float64 `json:"cost,omitempty"`
}

// StartIn is the input of run.start (v0.1.17 flags).
type StartIn struct {
	Task    string `json:"task,omitempty" flag:"task" desc:"task id (tsk_…): the run's span goes on this task's time when it ends"`
	Mission string `json:"mission,omitempty" flag:"mission" desc:"mission id (msn_…)"`
	Model   string `json:"model,omitempty" flag:"model" desc:"model identifier, as the agent reports it"`
	Agent   string `json:"agent,omitempty" flag:"agent" desc:"agent kind, free text (your coding agent's name, or manual)"`
	RetryOf string `json:"retry_of,omitempty" flag:"retry-of" desc:"previous run id this is retrying"`
	Goal    string `json:"goal,omitempty" flag:"goal" desc:"free-form description of what the run is trying to do"`
	Kind    string `json:"kind,omitempty" flag:"kind" desc:"run kind (default: 'task' if --task given, else 'general')"`
}

// EndIn is the input of run.end.
type EndIn struct {
	ID        string  `json:"id" arg:"0" desc:"the run id (run_…)"`
	Outcome   string  `json:"outcome" flag:"outcome" desc:"success | partial | failed | cancelled"`
	Summary   string  `json:"summary,omitempty" flag:"summary" desc:"human summary of what happened"`
	Error     string  `json:"error,omitempty" flag:"error" desc:"error message (for failed/partial outcomes)"`
	TokensIn  int     `json:"tokens_in,omitempty" flag:"tokens-in" desc:"total input tokens"`
	TokensOut int     `json:"tokens_out,omitempty" flag:"tokens-out" desc:"total output tokens"`
	Cost      float64 `json:"cost,omitempty" flag:"cost" desc:"total cost in USD"`
}

// CancelIn is the input of run.cancel.
type CancelIn struct {
	ID     string `json:"id" arg:"0" desc:"the run id (run_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why the run was cancelled"`
}

// StartOut is the output of run.start.
type StartOut struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// Text implements op.Texter: "<id> started" (TIME_TRACKING 2.2).
func (o StartOut) Text() string { return o.ID + " started\n" }

// EndOut is the output of run.end and run.cancel.
type EndOut struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
	// Seconds is the run's length; 0 with Elapsed "start time unknown"
	// when the run has no start.
	Seconds int64  `json:"seconds"`
	Elapsed string `json:"elapsed"`
	// TaskID is the task the span was written to ("" when none).
	TaskID string `json:"task_id,omitempty"`
	// TimeEntry is the id of the span written or queued ("" when none).
	TimeEntry string `json:"time_entry,omitempty"`
	// Queued: the task's store was unreachable, the span waits in the
	// time_outbox (TIME 11.9).
	Queued bool `json:"queued,omitempty"`
	// Notes are warnings the person should see (span not recorded, …).
	Notes []string `json:"notes,omitempty"`
	// cancelled picks the cancel line.
	cancelled bool
}

// Text implements op.Texter.
func (o EndOut) Text() string {
	var b strings.Builder
	if o.cancelled {
		b.WriteString(o.ID + " cancelled\n")
	} else {
		b.WriteString(o.ID + " ended  (" + o.Elapsed + ")\n")
	}
	for _, n := range o.Notes {
		b.WriteString(noteMark + n + "\n")
	}
	return b.String()
}

// Ops returns the run operations bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	writeErrs := []errs.Code{errs.InvalidInput, errs.NotFound, errs.NotImplemented, errs.Internal}
	return []op.Entry{
		op.Define(op.Spec{Name: OpStart, Summary: "Open a run (records what agent is attempting which task with which model)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "open a run on a task", CLI: `lore run start --task tsk_… --goal "add rate limit to login"`, Want: "started"}}}, o.start),
		op.Define(op.Spec{Name: OpEnd, Summary: "Close a run with an outcome (success | partial | failed | cancelled)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "close a run", CLI: "lore run end run_… --outcome success", Want: "ended"}}}, o.end),
		op.Define(op.Spec{Name: OpCancel, Summary: "Cancel a run (end it with outcome cancelled)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs}, o.cancel),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

func (o ops) start(ctx context.Context, in StartIn) (StartOut, error) {
	notes := Notes{TaskID: strings.TrimSpace(in.Task), MissionID: strings.TrimSpace(in.Mission), AgentKind: in.Agent,
		ModelID: in.Model, RetryOfRunID: in.RetryOf, Goal: in.Goal}
	// v0.1.17 kept an unresolvable --task as typed; a value that is not a
	// task id is refused now, because its span could never be written.
	if notes.TaskID != "" {
		if tid, err := ids.ParseAny(notes.TaskID); err != nil || tid.Prefix() != ids.PrefixTask {
			return StartOut{}, errs.New(errs.InvalidInput, "run start: --task "+strconv.Quote(notes.TaskID)+" is not a task id (tsk_…)")
		}
	}
	kind := in.Kind
	if kind == "" {
		kind = kindGeneral
		if notes.TaskID != "" {
			kind = kindTask
		}
	}
	nj, err := notesValue(notes)
	if err != nil {
		return StartOut{}, err
	}
	kv, err := value.TextOf(kind)
	if err != nil {
		return StartOut{}, errs.Wrap(errs.InvalidInput, "run start: --kind", err)
	}
	fields := map[string]value.Value{
		schema.FieldRunKind:      kv,
		schema.FieldRunStatus:    text(schema.RunInProgress),
		schema.FieldRunStartedAt: value.DateTimeFrom(opclock.Now(o.env)),
		schema.FieldRunNotes:     nj,
	}
	if a, ok := upgrade.As[Actorer](o.env); ok {
		actor, aerr := a.Actor(ctx)
		if aerr != nil {
			return StartOut{}, aerr
		}
		fields[schema.FieldRunActorID] = text(actor.String())
	}
	if p, ok := upgrade.As[Projecter](o.env); ok {
		// A repo with no project yet still logs runs (v0.1.17 needed one;
		// the machine store does not): project_id is then left empty.
		if prj, perr := p.Project(ctx); perr == nil {
			fields[schema.FieldRunProjectID] = text(prj.String())
		}
	}
	w, err := o.env.Create(ctx, store.SourceMachine, schema.TableRuns, fields)
	if err != nil {
		return StartOut{}, err
	}
	return StartOut{ID: w.Row.ID.String(), Kind: kind, Status: schema.RunInProgress}, nil
}

func (o ops) end(ctx context.Context, in EndIn) (EndOut, error) {
	if !oneOf(in.Outcome, schema.RunOutcomes()) {
		return EndOut{}, errs.New(errs.InvalidInput, "run end: --outcome "+strconv.Quote(in.Outcome)+" is not one of "+strings.Join(schema.RunOutcomes(), " | "))
	}
	return o.close(ctx, in.ID, func(n *Notes) {
		n.Outcome = in.Outcome
		if in.Summary != "" {
			n.Summary = in.Summary
		}
		if in.Error != "" {
			n.ErrorMessage = in.Error
		}
		// run_steps is dropped, so the totals are what the caller gives.
		n.TokensIn, n.TokensOut, n.Cost = in.TokensIn, in.TokensOut, in.Cost
	}, in.Outcome, false)
}

func (o ops) cancel(ctx context.Context, in CancelIn) (EndOut, error) {
	return o.close(ctx, in.ID, func(n *Notes) {
		n.Outcome = schema.RunCancelled
		if in.Reason != "" {
			n.Summary = in.Reason
		}
	}, schema.RunCancelled, true)
}

// close ends run id with status, after edit has updated its notes, then
// writes the run's span to its task (TIME 4.3).
func (o ops) close(ctx context.Context, id string, edit func(*Notes), status string, cancelled bool) (EndOut, error) {
	ref, cur, err := o.lookup(ctx, id)
	if err != nil {
		return EndOut{}, err
	}
	notes := notesOf(cur)
	edit(&notes)
	nj, err := notesValue(notes)
	if err != nil {
		return EndOut{}, err
	}
	// One reading of the clock for the stored end and the printed length.
	end := opclock.Now(o.env).UTC()
	if _, err = o.env.Update(ctx, ref, map[string]value.Value{
		schema.FieldRunStatus:      text(status),
		schema.FieldRunCompletedAt: value.DateTimeFrom(end),
		schema.FieldRunNotes:       nj,
	}); err != nil {
		return EndOut{}, err
	}
	out := EndOut{ID: ref.ID.String(), Status: status, Outcome: notes.Outcome, Elapsed: startUnknown, cancelled: cancelled}
	start := fieldTime(cur, schema.FieldRunStartedAt)
	if !start.IsZero() {
		d := end.Sub(start).Round(time.Second)
		out.Seconds, out.Elapsed = int64(d/time.Second), spaced(d)
	}
	if notes.TaskID == "" || start.IsZero() {
		return out, nil
	}
	out.TaskID = notes.TaskID
	// TIME 11.9: spans queued while a store was unreachable go first.
	if n := FlushOutbox(ctx, o.env); n.Failed != "" {
		out.Notes = append(out.Notes, n.Failed)
	}
	entry, queued, err := o.recordSpan(ctx, cur, notes.TaskID, start, end)
	switch {
	case err != nil:
		// The run stays ended: on this machine the run is the record of truth.
		out.Notes = append(out.Notes, "run time not recorded on task "+notes.TaskID+": "+err.Error())
	case queued:
		out.TimeEntry, out.Queued = entry, true
		out.Notes = append(out.Notes, "task "+notes.TaskID+"'s store is unreachable: run time queued, written on the next run end or lore sync")
	default:
		out.TimeEntry = entry
	}
	return out, nil
}

// lookup finds run id (E_NOT_FOUND for anything that is not a run's id).
func (o ops) lookup(ctx context.Context, id string) (engine.Ref, row.Row, error) {
	id = strings.TrimSpace(id)
	aid, err := ids.ParseAny(id)
	if err != nil || aid.Prefix() != ids.PrefixRun {
		return engine.Ref{}, row.Row{}, lookup.BadID("run "+strconv.Quote(id)+" not found (want run_…)", err)
	}
	ref := engine.Ref{Table: schema.TableRuns, ID: aid}
	src, r, err := o.env.Get(ctx, ref)
	if err != nil {
		return engine.Ref{}, row.Row{}, err
	}
	ref.Store = src
	return ref, r, nil
}

// recordSpan appends the run's span (kind agent, source run, local_ref the
// run id) to the task, or queues it when the task's store is unreachable.
func (o ops) recordSpan(ctx context.Context, run row.Row, taskID string, start, end time.Time) (entryID string, queued bool, err error) {
	tid, err := ids.ParseAny(taskID)
	if err != nil {
		return "", false, err
	}
	actor, err := ids.Parse[ids.Actor](fieldText(run, schema.FieldRunActorID))
	if err != nil {
		return "", false, errs.New(errs.InvalidInput, "the run has no actor")
	}
	entry, err := timeentry.NewSpan(timeentry.Span{Kind: timeentry.KindAgent, Source: timeentry.SourceRun, Actor: actor,
		Start: start, End: end, LocalRef: run.ID.String()})
	if err != nil {
		return "", false, err
	}
	err = appendEntry(ctx, o.env, tid, entry)
	if !errs.Has(err, errs.StoreUnreachable) {
		return entry.ID.String(), false, err
	}
	if qerr := queue(ctx, o.env, taskID, entry); qerr != nil {
		return "", false, errors.Join(err, qerr)
	}
	return entry.ID.String(), true, nil
}

// appendEntry appends entry to task tid's time_entries; an entry already
// there is not added again (an outbox replay is idempotent).
func appendEntry(ctx context.Context, env Env, tid ids.AnyID, entry timeentry.Entry) error {
	ref := engine.Ref{Table: schema.TableTasks, ID: tid}
	src, cur, err := env.Get(ctx, ref)
	if err != nil {
		return err
	}
	for _, e := range opstask.TimeEntries(cur) {
		if e.ID == entry.ID {
			return nil
		}
	}
	list, err := opstask.WithTimeEntry(cur, entry)
	if err != nil {
		return err
	}
	ref.Store = src
	_, err = env.Update(ctx, ref, map[string]value.Value{opstask.FieldTimeEntries: list})
	return err
}

// notesOf decodes a run's notes; a missing or unreadable value is empty
// notes (a hand-edited row must not stop the run from ending).
func notesOf(r row.Row) Notes {
	var n Notes
	if s := fieldText(r, schema.FieldRunNotes); s != "" {
		if err := json.Unmarshal([]byte(s), &n); err != nil {
			return Notes{}
		}
	}
	return n
}

// notesValue is the notes as the text value stored in the row.
func notesValue(n Notes) (value.Value, error) {
	b, err := json.Marshal(n)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "run: encode notes", err)
	}
	return text(string(b)), nil
}

// text is a text value for strings lore built itself (statuses, ids,
// encoded JSON): value.TextOf refuses only invalid UTF-8, which none of
// them can hold, so the unreachable error maps to the empty Text rather
// than being threaded through every map literal. Caller-typed text goes
// through value.TextOf directly.
func text(s string) value.Value {
	t, err := value.TextOf(s)
	if err != nil {
		return value.Text{}
	}
	return t
}

// fieldText is a row field's text ("" when absent or null).
func fieldText(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// fieldTime is a datetime field (zero when absent).
func fieldTime(r row.Row, name string) time.Time {
	switch v := r.Fields[name].(type) {
	case value.DateTime:
		return v.Time().UTC()
	case value.Text:
		if dt, err := value.DateTimeOf(v.String()); err == nil {
			return dt.Time().UTC()
		}
	}
	return time.Time{}
}

func oneOf(s string, set []string) bool {
	for _, v := range set {
		if s == v {
			return true
		}
	}
	return false
}

// spaced is d as "45m 10s" (TIME_TRACKING 2.2): Go's "45m10s" with a space
// after each unit but the last.
func spaced(d time.Duration) string {
	s := d.String()
	var b strings.Builder
	for i, r := range s {
		b.WriteRune(r)
		if (r == 'h' || r == 'm') && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
