// Package reminder defines the `reminder` noun's operations (layer L5):
// add, list, done and delete, with v0.1.17's flag spellings (`lore reminder
// add --due YYYY-MM-DD [--recurrence 7d] [--on-table --on-id] <message>`).
//
// A reminder is synced work tracking (v0.1.17 lsync syncedTables). `done`
// on a recurring reminder moves its due date forward by the recurrence
// instead of completing it; a one-shot reminder gets done_at.
//
// Every write goes through Env.Create / Update / Delete (the engine's one
// write path: validation, secret scan, policy, audit).
package reminder

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore reminder …`, REST /v1/reminders).
const (
	Noun     = "reminder"
	OpAdd    = "reminder.add"
	OpList   = "reminder.list"
	OpDone   = "reminder.done"
	OpDelete = "reminder.delete"
)

// Envelope values of v0.1.17's --json.
const (
	SchemaVersion = 1
	KindList      = "reminder.list"
)

// listLimit is v0.1.17's fixed page (Limit(100), oldest due first).
const listLimit = 100

// table is the reminders table.
const table row.Table = schema.TableReminders

// overdueMark is v0.1.17's marker for a pending reminder past its due date
// (style.Error("!") without colour); notDueMark pads the column.
const (
	overdueMark = "!"
	notDueMark  = " "
)

// Env is what the reminder operations use; *wire.App implements it.
type Env = work.Env

// Actorer is the optional Env upgrade that names the current identity, so
// a new reminder is stamped with created_by_actor_id as v0.1.17 did. An
// Env without it leaves the field to the envelope's created_by.
type Actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// recurrenceShift is how far `done` moves each recurrence's due date
// (v0.1.17 recurrenceShift). The keys are exactly
// schema.ReminderRecurrences(); a stored value outside them is refused by
// the schema's OneOf on write.
var recurrenceShift = map[string]struct{ y, m, d int }{
	schema.Recurrence7d:  {0, 0, 7},
	schema.Recurrence30d: {0, 0, 30},
	schema.Recurrence1m:  {0, 1, 0},
	schema.Recurrence3m:  {0, 3, 0},
	schema.Recurrence6m:  {0, 6, 0},
	schema.Recurrence1y:  {1, 0, 0},
}

// bumpRecurrence advances from by the recurrence pattern; ok is false for
// a pattern outside schema.ReminderRecurrences() (v0.1.17 panicked there;
// a hand-edited row file must not crash the CLI).
func bumpRecurrence(from time.Time, pattern string) (time.Time, bool) {
	s, ok := recurrenceShift[pattern]
	if !ok {
		return time.Time{}, false
	}
	return from.AddDate(s.y, s.m, s.d), true
}

// AddIn is the input of reminder.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Message    string `json:"message" arg:"0" rest:"true" desc:"the reminder text (several words are joined)"`
	Due        string `json:"due" flag:"due" desc:"due date YYYY-MM-DD or RFC3339 (required)"`
	Recurrence string `json:"recurrence,omitempty" flag:"recurrence" desc:"recurrence: 7d | 30d | 1m | 3m | 6m | 1y"`
	OnTable    string `json:"on_table,omitempty" flag:"on-table" desc:"optional target entity table"`
	OnID       string `json:"on_id,omitempty" flag:"on-id" desc:"optional target entity id"`
	CreatedBy  string `json:"created_by,omitempty" flag:"created-by" desc:"actor_id (act_*); defaults to current identity"`
	Repo       string `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts; reminders have no repo scope"`
	To         string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
}

// ListIn is the input of reminder.list.
type ListIn struct {
	Done  bool   `json:"done,omitempty" flag:"done" desc:"show completed instead of pending"`
	Repo  string `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts; reminders have no repo scope"`
	Store string `json:"store,omitempty" flag:"store" desc:"only reminders of this store (default every attached store)"`
	// MetaFlags: --meta / --place / --sort meta.<key> (METADATA 3.7).
	work.MetaFlags
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Notes are the metadata filter's lines (METADATA 3.7, 3.13).
	Notes []string `json:"notes,omitempty"`
	// now is when the list was read (the overdue marker compares to it).
	now time.Time
}

// Record is one reminder in v0.1.17's --json form (its keys, sorted).
type Record struct {
	DoneAt     string          `json:"done_at,omitempty"`
	DueAt      string          `json:"due_at"`
	ID         string          `json:"id"`
	Message    string          `json:"message"`
	Recurrence string          `json:"recurrence,omitempty"`
	Store      store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	due   time.Time
}

// Text is v0.1.17's list: "<mark> <due>  <message>[ (<recurrence>)]" then
// the id indented on its own line.
func (o ListOut) Text() string { return o.rowsText() + work.NotesText(o.Notes) }

// rowsText is the list lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no reminders)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		mark := notDueMark
		if r.DoneAt == "" && r.due.Before(o.now) {
			mark = overdueMark
		}
		rec := ""
		if r.Recurrence != "" {
			rec = " (" + r.Recurrence + ")"
		}
		fmt.Fprintf(&b, "%s %s  %s%s\n    %s\n", mark, r.due.UTC().Format(work.DateLayout), r.Message, rec, r.ID)
	}
	return b.String()
}

// IDIn is the input of reminder.done and reminder.delete.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the reminder id (rmd_…)"`
}

// DeleteIn is the input of reminder.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the reminder id (rmd_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
	// PurgeFlags: --purge / --confirm (engine binds them, P3-DEL-1).
	op.PurgeFlags
}

// DoneOut is the output of reminder.done.
type DoneOut struct {
	ID       string         `json:"id"`
	Store    string         `json:"store"`
	Warnings []work.Warning `json:"warnings,omitempty"`
	// Rescheduled is the new due date (YYYY-MM-DD) of a recurring reminder;
	// empty when the reminder was completed.
	Rescheduled string `json:"rescheduled,omitempty"`
	human       string
}

// Text is v0.1.17's line ("✓ rescheduled to <date>" or "✓ reminder <id>
// completed").
func (o DoneOut) Text() string { return o.human }

// doneOut copies a write's output into a DoneOut.
func doneOut(w work.WriteOut, rescheduled string) DoneOut {
	return DoneOut{ID: w.ID, Store: w.Store, Warnings: w.Warnings, Rescheduled: rescheduled, human: w.Human}
}

// Ops returns every reminder operation bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a reminder (--due YYYY-MM-DD)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "a weekly review",
				CLI: `lore reminder add --due 2026-12-01 --recurrence 7d "weekly review"`, Want: "✓ reminder rmd_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List pending reminders (or --done to see completed)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
			Examples: []op.Example{{Title: "pending reminders", CLI: "lore reminder list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpDone, Summary: "Mark a reminder done (recurring → reschedules)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, o.done),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a reminder (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors,
			Confirm:  op.ConfirmRule{Always: true},
			Examples: []op.Example{{Title: "delete without a terminal", CLI: "lore reminder delete rmd_<id> --yes", Want: "moved to the trash"}},
		}, o.delete),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// add validates the flags and writes the reminder.
func (o ops) add(ctx context.Context, in AddIn) (work.WriteOut, error) {
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		return work.WriteOut{}, errs.New(errs.EmptyBody, "reminder add: the message is empty").
			WithHint(`lore reminder add --due YYYY-MM-DD "<message>"`)
	}
	if strings.TrimSpace(in.Due) == "" {
		return work.WriteOut{}, errs.New(errs.InvalidInput, `required flag(s) "due" not set`)
	}
	due, err := parseDue(in.Due)
	if err != nil {
		return work.WriteOut{}, err
	}
	if in.Recurrence != "" && !slices.Contains(schema.ReminderRecurrences(), in.Recurrence) {
		return work.WriteOut{}, errs.New(errs.InvalidInput,
			fmt.Sprintf("bad --recurrence %q (want %s)", in.Recurrence, strings.Join(schema.ReminderRecurrences(), " | ")))
	}
	fields, err := work.Texts(Noun, map[string]string{
		schema.FieldReminderMessage:     msg,
		schema.FieldReminderRecurrence:  in.Recurrence,
		schema.FieldReminderTargetTable: in.OnTable,
		schema.FieldReminderTargetID:    in.OnID,
	})
	if err != nil {
		return work.WriteOut{}, err
	}
	fields[schema.FieldReminderDueAt] = value.DateTimeFrom(due)
	if terr := o.checkTarget(ctx, store.Source(in.To), in.OnID); terr != nil {
		return work.WriteOut{}, terr
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return work.WriteOut{}, err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return work.WriteOut{}, err
	}
	if v, rerr := work.RefTo(Noun, "created-by", ids.PrefixActor, in.CreatedBy); rerr != nil {
		return work.WriteOut{}, rerr
	} else if v != nil {
		fields[work.FieldCreatedBy] = v
	} else if v := o.currentActor(ctx); v != nil {
		fields[work.FieldCreatedBy] = v
	}
	src, w, err := work.Create(ctx, o.env, in.To, table, fields)
	if err != nil {
		return work.WriteOut{}, err
	}
	human := work.Success + " reminder " + w.Row.ID.String() + " due " + due.UTC().Format(work.DateLayout) + "\n"
	return work.Out(src, w, human), nil
}

// parseDue reads --due as YYYY-MM-DD, else RFC3339 (v0.1.17's order and
// error text).
func parseDue(s string) (time.Time, error) {
	t, err := time.Parse(work.DateLayout, s)
	if err == nil {
		return t, nil
	}
	t, err = time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errs.Wrap(errs.InvalidInput, "bad --due (want YYYY-MM-DD)", err)
	}
	return t, nil
}

// currentActor is a ref to the current identity's actor row, or nil when
// the Env cannot name one or the row does not exist (the envelope's
// created_by still records who wrote it).
func (o ops) currentActor(ctx context.Context) value.Value {
	a, ok := o.env.(Actorer)
	if !ok {
		return nil
	}
	actor, err := a.Actor(ctx)
	if err != nil || actor.IsZero() {
		return nil
	}
	if _, _, gerr := work.Get(ctx, o.env, "actor", schema.TableActors, actor.Any()); gerr != nil {
		return nil
	}
	v, err := value.RefOf(actor.Any())
	if err != nil {
		return nil
	}
	return v
}

// list returns pending (or done) reminders of every attached store, oldest
// due first, at most listLimit (v0.1.17).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, table) // DELETION 3.2: --include-deleted / --deleted-only
	rows, err := work.ListAll(ctx, o.env, table, in.Store)
	if err != nil {
		return ListOut{}, err
	}
	rows, notes, err := work.Narrow(ctx, o.env, rows, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}, now: opclock.Now(o.env), Notes: notes}
	for _, s := range rows {
		done := !work.Time(s.Row, schema.FieldReminderDoneAt).IsZero()
		if done != in.Done {
			continue
		}
		out.Data = append(out.Data, recordOf(work.LabelerOf(o.env), s))
	}
	if in.Sort == "" { // --sort meta.<key> replaces the due order (3.7)
		slices.SortStableFunc(out.Data, func(a, b Record) int { return a.due.Compare(b.due) })
	}
	if len(out.Data) > listLimit {
		out.Data = out.Data[:listLimit]
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of one reminder row.
func recordOf(lab work.Labeler, s work.Sourced) Record {
	due := work.Time(s.Row, schema.FieldReminderDueAt)
	rec := Record{
		DueAt: due.UTC().Format(time.RFC3339), ID: s.Row.ID.String(),
		Message:    work.Field(s.Row, schema.FieldReminderMessage),
		Recurrence: work.Field(s.Row, schema.FieldReminderRecurrence),
		Store:      work.SourceOf(lab, s.Source), Label: work.Label(lab, s.Source), due: due,
	}
	if t := work.Time(s.Row, schema.FieldReminderDoneAt); !t.IsZero() {
		rec.DoneAt = t.UTC().Format(time.RFC3339)
	}
	return rec
}

// done completes a one-shot reminder or moves a recurring one forward.
func (o ops) done(ctx context.Context, in IDIn) (DoneOut, error) {
	id, err := work.ParseID(Noun, ids.PrefixReminder, in.ID)
	if err != nil {
		return DoneOut{}, err
	}
	_, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return DoneOut{}, err
	}
	if rec := work.Field(r, schema.FieldReminderRecurrence); rec != "" {
		next, ok := bumpRecurrence(work.Time(r, schema.FieldReminderDueAt), rec)
		if !ok {
			return DoneOut{}, errs.New(errs.InvalidInput, "reminder "+id.String()+": unknown recurrence "+work.Quote(rec)).
				WithHint("fix it with a recurrence of: " + strings.Join(schema.ReminderRecurrences(), " | "))
		}
		human := work.Success + " rescheduled to " + next.UTC().Format(work.DateLayout) + "\n"
		w, perr := work.Patch(ctx, o.env, Noun, table, id, map[string]value.Value{schema.FieldReminderDueAt: value.DateTimeFrom(next)}, human)
		if perr != nil {
			return DoneOut{}, perr
		}
		return doneOut(w, next.UTC().Format(work.DateLayout)), nil
	}
	human := work.Success + " reminder " + id.String() + " completed\n"
	w, err := work.Patch(ctx, o.env, Noun, table, id, map[string]value.Value{schema.FieldReminderDoneAt: work.Now(o.env)}, human)
	if err != nil {
		return DoneOut{}, err
	}
	return doneOut(w, ""), nil
}

// delete writes the reminder's tombstone (v0.1.17 hard-deleted it; a
// tombstone keeps an old copy from coming back on the next sync).
func (o ops) delete(ctx context.Context, in DeleteIn) (work.WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	id, err := work.ParseID(Noun, ids.PrefixReminder, in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Delete(ctx, o.env, Noun, table, id, trashline.Moved(id.String(), id.String()))
}

// checkTarget applies F15 (ops/internal/pointer) to --on-id: target_id is
// free text (a v0.1.17 value need not be an id, so it is not a schema
// ref the engine checks), but when it holds an id of a core table it is a
// pointer like any other, so a client reminder may not name an internal
// or personal row. A value that is not such an id is left as v0.1.17 left it.
func (o ops) checkTarget(ctx context.Context, to store.Source, raw string) error {
	id, err := ids.ParseAny(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return errs.Wrap(errs.Internal, "reminder: built-in schema", err)
	}
	def, ok := reg.ByPrefix(id.Prefix())
	if !ok {
		return nil
	}
	return pointer.Check(ctx, o.env, to, engine.Ref{Table: def.Name, ID: id})
}
