// Package plan defines the `plan` noun's operations on the new core: add,
// list, show, edit, delete and search of plan DOCUMENTS (TSQ9: `lore plan`
// means plan documents only; the YAML import is `lore mission apply`), with
// v0.1.17's flags and output (saas/cmd/cli/generated_plan_tasklist.go at
// 4e6ed1c). v0.1.17's free `status_str` is `status` with the closed set
// schema.PlanStatuses; a `_v: 1` row's status_str is upcast on read
// (UpcastStatus) and cleared by the first write through the core.
package plan

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (plan.add → `lore plan add`, POST /v1/plans).
const (
	// Noun is the CLI noun of every plan operation.
	Noun     = "plan"
	OpAdd    = "plan.add"
	OpList   = "plan.list"
	OpShow   = "plan.show"
	OpEdit   = "plan.edit"
	OpDelete = "plan.delete"
	OpSearch = "plan.search"
)

// Envelope kinds (v0.1.17).
const (
	KindList   = "plan.list"
	KindShow   = "plan.show"
	KindSearch = "plan.search"
)

// Defaults and names.
const (
	// DefaultStatus is a new plan's status (TSQ9).
	DefaultStatus = schema.PlanStatusDraft
	fieldStatus   = work.FieldStatus
	// badge prefixes a list line ("PL  <id>", v0.1.17's generated output).
	badge = "PL"
	// legacyActive is v0.1.17's only written status_str.
	legacyActive = "active"
)

// table is the plans table.
const table row.Table = schema.TablePlans

// UpcastStatus maps a v0.1.17 plans.status_str to the TSQ9 closed set: a
// value already in the set is kept, v0.1.17's default "active" becomes
// in_progress, and anything else (free text, empty) becomes draft (open
// question F2 in FORMAT_EVOLUTION_DESIGN; spec-deltas records this rule).
func UpcastStatus(statusStr string) string {
	s := strings.TrimSpace(strings.ToLower(statusStr))
	switch {
	case slices.Contains(schema.PlanStatuses(), s):
		return s
	case s == legacyActive:
		return schema.PlanStatusInProgress
	}
	return schema.PlanStatusDraft
}

// statusOf is a plan row's status: the status field, else the upcast
// v0.1.17 status_str.
func statusOf(r row.Row) string {
	if s := work.Field(r, fieldStatus); s != "" {
		return s
	}
	return UpcastStatus(work.Field(r, schema.FieldPlanStatusStr))
}

// AddIn is the input of plan.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Title string `json:"title" flag:"title" desc:"title (required)"`
	// Body is required, by --body or as the first argument.
	Body      string `json:"body,omitempty" flag:"body" desc:"body (required; or give it as the first argument)" ui:"widget=markdown"`
	Text      string `json:"text,omitempty" arg:"0" desc:"the body, instead of --body"`
	CreatedBy string `json:"created_by,omitempty" flag:"created-by" desc:"actor_id; default: the row's created_by is the current identity"`
	To        string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	Project   string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// Record is the ent Plan JSON (v0.1.17 --json, status_str now status).
type Record struct {
	ID               string          `json:"id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	Title            string          `json:"title,omitempty"`
	Body             string          `json:"body,omitempty"`
	Status           string          `json:"status,omitempty"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Edges            Edges           `json:"edges"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// Edges is ent's edges object: v0.1.17 printed it empty.
type Edges struct{}

// ShowOut is the output of plan.show and plan.add.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Human is the add line; show prints "PL  <id>".
	Human string `json:"-"`
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
}

// Text implements op.Texter.
func (o ShowOut) baseText() string {
	if o.Human != "" {
		return o.Human
	}
	return badge + "  " + o.Data.ID + "\n"
}

// ListIn is the input of plan.list.
type ListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only plans of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only plans of this store (default every attached store)"`
	// MetaFlags: --meta / --place / --sort meta.<key> (METADATA 3.7).
	work.MetaFlags
}

// ListOut is the output of plan.list.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Notes are the metadata filter's lines (METADATA 3.7, 3.13).
	Notes []string `json:"notes,omitempty"`
}

// Text prints "PL  <id>" per plan; "(no plans)" when none.
func (o ListOut) Text() string { return o.rowsText() + work.NotesText(o.Notes) }

// rowsText is the list lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no plans)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(badge + "  " + r.ID + "\n")
	}
	return b.String()
}

// IDIn is the input of the operations that take one plan id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the plan id (pln_…)"`
}

// DeleteIn is the input of plan.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the plan id (pln_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// EditIn is the input of plan.edit (only the flags given change).
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID     string `json:"id" arg:"0" desc:"the plan id (pln_…)"`
	Title  string `json:"title,omitempty" flag:"title" desc:"new title"`
	Body   string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Status string `json:"status,omitempty" flag:"status" desc:"draft | approved | in_progress | done | dropped"`
}

// SearchIn is the input of plan.search.
type SearchIn struct {
	Query   string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Limit   int    `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	Project string `json:"project,omitempty" flag:"project" desc:"only plans of this project (prj_ id or name)"`
	// Columns restricts the match to some full-text fields (v0.1.17
	// --column).
	Columns []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable; plans have: title, body, status)"`
}

// Ops returns every plan operation bound to env.
func Ops(env work.Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new plan",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a plan document", CLI: `lore plan add --title "Invite flow" --body "1. backend 2. landing page"`, Want: "pln_"}},
		}, o.add),
		op.Define(op.Spec{Name: OpList, Summary: "List plans", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.list),
		op.Define(op.Spec{Name: OpShow, Summary: "Show plan details", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit plan fields (only flags you pass are applied)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors, Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a plan (its tasks and missions are detached; a tombstone is kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors, Confirm: op.ConfirmRule{Always: true},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across plan title + body + status",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.SearchErrors,
		}, o.search),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env work.Env }

// listID parses a plan id argument.
func listID(s string) (ids.AnyID, error) { return work.ParseID(Noun, ids.PrefixPlan, s) }

// recordOf is the v0.1.17 JSON of a plan row.
func recordOf(lab work.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ProjectID: work.Field(r, work.FieldProject),
		Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody), Status: statusOf(r),
		CreatedByActorID: work.Field(r, work.FieldCreatedBy), TagIDs: work.TagIDs(r), Store: work.SourceOf(lab, src), Label: work.Label(lab, src),
	}
}

// add creates one plan document.
func (o ops) add(ctx context.Context, in AddIn) (ShowOut, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "plan add: --title is required")
	}
	if in.Body != "" && in.Text != "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "plan add: give the body once, as an argument or with --body, not both")
	}
	body := in.Body + in.Text
	if strings.TrimSpace(body) == "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "body required").WithHint(`pass --body="<text>"`)
	}
	project, err := work.Project(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ShowOut{}, err
	}
	fields, err := work.Texts(Noun, map[string]string{work.FieldTitle: title, work.FieldBody: body, fieldStatus: DefaultStatus})
	if err != nil {
		return ShowOut{}, err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return ShowOut{}, err
	}
	if v, rerr := work.RefTo(Noun, "created-by", ids.PrefixActor, in.CreatedBy); rerr != nil {
		return ShowOut{}, rerr
	} else if v != nil {
		fields[work.FieldCreatedBy] = v
	}
	src, w, err := work.Create(ctx, o.env, in.To, table, fields)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: recordOf(work.LabelerOf(o.env), src, w.Row), Human: work.Success + " " + w.Row.ID.String() + "\n"}, nil
}

// list returns the plans of every attached store.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	all, err := work.ListAll(ctx, o.env, table, in.Store)
	if err != nil {
		return ListOut{}, err
	}
	all, notes, err := work.Narrow(ctx, o.env, all, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: work.SchemaVersion, Kind: KindList, Data: []Record{}, Notes: notes}
	for _, s := range all {
		if project == "" || work.Field(s.Row, work.FieldProject) == project {
			out.Data = append(out.Data, recordOf(work.LabelerOf(o.env), s.Source, s.Row))
		}
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one plan.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	id, err := listID(in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: recordOf(work.LabelerOf(o.env), src, r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (work.WriteOut, error) {
	id, err := listID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	if in.Status != "" {
		if serr := work.OneOf(Noun, "status", in.Status, schema.PlanStatuses()); serr != nil {
			return work.WriteOut{}, serr
		}
	}
	patch, err := work.Texts(Noun, map[string]string{work.FieldTitle: strings.TrimSpace(in.Title), work.FieldBody: in.Body, fieldStatus: in.Status})
	if err != nil {
		return work.WriteOut{}, err
	}
	if len(patch) == 0 {
		return work.WriteOut{}, errs.New(errs.InvalidInput, "plan edit: nothing to change").WithHint("give at least one of --title, --body, --status")
	}
	// A v0.1.17 row: move its status_str into status in the same write
	// (TSQ9), unless --status sets it.
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	if legacy := work.Field(cur, schema.FieldPlanStatusStr); legacy != "" {
		patch[schema.FieldPlanStatusStr] = value.Null{}
		if _, set := patch[fieldStatus]; !set && work.Field(cur, fieldStatus) == "" {
			if patch[fieldStatus], err = value.TextOf(UpcastStatus(legacy)); err != nil {
				return work.WriteOut{}, err
			}
		}
	}
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" plan "+id.String()+" updated\n")
}

// delete tombstones the plan; its tasks' and missions' plan_id is cleared.
func (o ops) delete(ctx context.Context, in DeleteIn) (work.WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	id, err := listID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Delete(ctx, o.env, Noun, table, id, work.Success+" "+id.String()+" deleted\n")
}

// search matches plans by title, body and status.
func (o ops) search(ctx context.Context, in SearchIn) (work.SearchEnvelope, error) {
	return work.SearchTable(ctx, o.env, table, KindSearch, in.Query, in.Limit, in.Project, in.Columns, false, work.FieldTitle, work.FieldBody, fieldStatus)
}
