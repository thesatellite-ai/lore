// Package tasklist defines the `tasklist` noun's operations on the new
// core: add, list, show, edit, delete and search, with v0.1.17's flags and
// output (saas/cmd/cli/generated_plan_tasklist.go at 4e6ed1c).
package tasklist

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (tasklist.add → `lore tasklist add`, POST /v1/tasklists).
const (
	// Noun is the CLI noun of every tasklist operation.
	Noun     = "tasklist"
	OpAdd    = "tasklist.add"
	OpList   = "tasklist.list"
	OpShow   = "tasklist.show"
	OpEdit   = "tasklist.edit"
	OpDelete = "tasklist.delete"
	OpSearch = "tasklist.search"
)

// Envelope kinds (v0.1.17 spelled the entity taskList).
const (
	KindList = "taskList.list"
	KindShow = "taskList.show"
	// KindSearch is lowercase: v0.1.17's search envelope used the FTS entity name
	// (tasklist), unlike its list/show envelopes.
	KindSearch = "tasklist.search"
)

// Defaults and names.
const (
	// DefaultStatus is a new list's status_str (v0.1.17 wrote "active").
	DefaultStatus = "active"
	fieldStatus   = "status_str"
	// badge prefixes a list line ("TA  <id>", v0.1.17's generated output).
	badge = "TA"
)

// table is the tasklists table.
const table row.Table = schema.TableTasklists

// AddIn is the input of tasklist.add.
type AddIn struct {
	Title string `json:"title" flag:"title" desc:"title (required)"`
	// Body is required, by --body or as the first argument.
	Body    string `json:"body,omitempty" flag:"body" desc:"body (required; or give it as the first argument)" ui:"widget=markdown"`
	Text    string `json:"text,omitempty" arg:"0" desc:"the body, instead of --body"`
	To      string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// Record is the ent TaskList JSON (v0.1.17 --json).
type Record struct {
	ID        string    `json:"id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	Title     string    `json:"title,omitempty"`
	Body      string    `json:"body,omitempty"`
	StatusStr string    `json:"status_str,omitempty"`
	TagIDs    []string  `json:"tag_ids,omitempty"`
	Edges     Edges     `json:"edges"`
	Store     string    `json:"_source,omitempty"`
}

// Edges is ent's edges object: v0.1.17 printed it empty.
type Edges struct{}

// ShowOut is the output of tasklist.show and tasklist.add.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Human is the add line; show prints "TA  <id>".
	Human string `json:"-"`
}

// Text implements op.Texter.
func (o ShowOut) Text() string {
	if o.Human != "" {
		return o.Human
	}
	return badge + "  " + o.Data.ID + "\n"
}

// ListIn is the input of tasklist.list.
type ListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only lists of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only lists of this store (default every attached store)"`
}

// ListOut is the output of tasklist.list.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// Text prints "TA  <id>" per list; "(no tasklists)" when none.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no tasklists)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(badge + "  " + r.ID + "\n")
	}
	return b.String()
}

// IDIn is the input of the operations that take one tasklist id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the tasklist id (tlt_…)"`
}

// EditIn is the input of tasklist.edit (only the flags given change).
type EditIn struct {
	ID     string `json:"id" arg:"0" desc:"the tasklist id (tlt_…)"`
	Title  string `json:"title,omitempty" flag:"title" desc:"new title"`
	Body   string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Status string `json:"status,omitempty" flag:"status" desc:"free-form status"`
}

// SearchIn is the input of tasklist.search.
type SearchIn struct {
	Query   string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Limit   int    `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	Project string `json:"project,omitempty" flag:"project" desc:"only lists of this project (prj_ id or name)"`
	// Columns restricts the match to some full-text fields (v0.1.17
	// --column).
	Columns []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable; tasklists have: title, body, status_str)"`
}

// Ops returns every tasklist operation bound to env.
func Ops(env work.Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new tasklist",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a list", CLI: `lore tasklist add --title "Sprint 4" --body "two weeks"`, Want: "tlt_"}},
		}, o.add),
		op.Define(op.Spec{Name: OpList, Summary: "List tasklists", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.list),
		op.Define(op.Spec{Name: OpShow, Summary: "Show tasklist details", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.show),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit tasklist fields",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors, Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a tasklist (its tasks are detached; a tombstone is kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors, Confirm: op.ConfirmRule{Always: true},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across tasklist title + body + status",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.SearchErrors,
		}, o.search),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env work.Env }

// listID parses a tasklist id argument.
func listID(s string) (ids.AnyID, error) { return work.ParseID(Noun, ids.PrefixTaskList, s) }

// recordOf is the v0.1.17 JSON of a tasklist row.
func recordOf(src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ProjectID: work.Field(r, work.FieldProject),
		Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody), StatusStr: work.Field(r, fieldStatus),
		TagIDs: work.TagIDs(r), Store: work.SourceName(src),
	}
}

// add creates one tasklist.
func (o ops) add(ctx context.Context, in AddIn) (ShowOut, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "tasklist add: --title is required")
	}
	if in.Body != "" && in.Text != "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "tasklist add: give the body once, as an argument or with --body, not both")
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
	src, w, err := work.Create(ctx, o.env, in.To, table, fields)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: recordOf(src, w.Row), Human: work.Success + " " + w.Row.ID.String() + "\n"}, nil
}

// list returns the tasklists of every attached store.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	project, err := work.ProjectFilter(ctx, o.env, Noun, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	all, err := work.ListAll(ctx, o.env, table, in.Store)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: work.SchemaVersion, Kind: KindList, Data: []Record{}}
	for _, s := range all {
		if project == "" || work.Field(s.Row, work.FieldProject) == project {
			out.Data = append(out.Data, recordOf(s.Source, s.Row))
		}
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one tasklist.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	id, err := listID(in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: recordOf(src, r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (work.WriteOut, error) {
	id, err := listID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	patch, err := work.Texts(Noun, map[string]string{work.FieldTitle: strings.TrimSpace(in.Title), work.FieldBody: in.Body, fieldStatus: in.Status})
	if err != nil {
		return work.WriteOut{}, err
	}
	if len(patch) == 0 {
		return work.WriteOut{}, errs.New(errs.InvalidInput, "tasklist edit: nothing to change").WithHint("give at least one of --title, --body, --status")
	}
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" tasklist "+id.String()+" updated\n")
}

// delete tombstones the tasklist; its tasks' tasklist_id is cleared.
func (o ops) delete(ctx context.Context, in IDIn) (work.WriteOut, error) {
	id, err := listID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Delete(ctx, o.env, Noun, table, id, work.Success+" "+id.String()+" deleted\n")
}

// search matches tasklists by title, body and status.
func (o ops) search(ctx context.Context, in SearchIn) (work.SearchEnvelope, error) {
	return work.SearchTable(ctx, o.env, table, KindSearch, in.Query, in.Limit, in.Project, in.Columns, false, work.FieldTitle, work.FieldBody, fieldStatus)
}
