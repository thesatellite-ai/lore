// Package tasklist defines the `tasklist` noun's operations on the new
// core: add, list, show, edit, delete and search, with v0.1.17's flags and
// output (saas/cmd/cli/generated_plan_tasklist.go at 4e6ed1c).
package tasklist

import (
	"context"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/wheredel"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"

	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	opstask "github.com/thesatellite-ai/lore/ops/task"
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
	// KindAdd is tasklist add's envelope, lowercase like v0.1.17's
	// printJSON(constants.KindTasklistAdd).
	KindAdd = "tasklist.add"
)

// Defaults and names.
const (
	// DefaultStatus is a new list's status_str (v0.1.17 wrote "active").
	DefaultStatus = "active"
	fieldStatus   = "status_str"
	// whereStatus is the --where key of fieldStatus: "status", the name
	// --status and the help use (wheredel.Spec.Aliases).
	whereStatus = "status"
	// badge opens the show's first line ("TA  <id>", v0.1.17's generated output).
	badge = "TA"
)

// table is the tasklists table.
const table row.Table = schema.TableTasklists

// AddIn is the input of tasklist.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	// Title is --title, as on every add (U-4 reverts U-3's positional
	// title: named args everywhere).
	Title string `json:"title" flag:"title" desc:"title (required)"`
	// Body is optional, by --body (or, quietly, as the first argument: the
	// v0.1.17 form `--title X "<body>"`).
	Body    string `json:"body,omitempty" flag:"body" desc:"body (optional)" ui:"widget=markdown"`
	Text    string `json:"text,omitempty" arg:"0" argof:"body" desc:"the body, instead of --body"`
	To      string `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// Record is the ent TaskList JSON (v0.1.17 --json).
type Record struct {
	ID        string          `json:"id,omitempty"`
	CreatedAt time.Time       `json:"created_at,omitempty"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
	ProjectID string          `json:"project_id,omitempty"`
	Title     string          `json:"title,omitempty"`
	Body      string          `json:"body,omitempty"`
	StatusStr string          `json:"status_str,omitempty"`
	TagIDs    []string        `json:"tag_ids,omitempty"`
	Edges     Edges           `json:"edges"`
	Store     store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Meta / MetaMasked are the row's metadata (METADATA 3.17 "metadata
	// on every read", MQ13, G-META-10 via metashow.Row): absent for a row
	// without metadata, so v0.1.17's record is unchanged.
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the v0.1.17 keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Edges is ent's edges object: v0.1.17 printed it empty.
type Edges struct{}

// ShowOut is the output of tasklist.show and tasklist.add.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Human is the add line; show prints the id, title, status, task
	// count and body (work.ShowText).
	Human string `json:"-"`
	// tasks is the live task count show prints (human only: the JSON
	// record is v0.1.17's).
	tasks int
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// Text implements op.Texter.
func (o ShowOut) baseText() string {
	if o.Human != "" {
		return o.Human
	}
	return work.ShowText(badge, o.Data.ID, o.Data.Title, o.Data.StatusStr, o.tasks, o.Data.Body)
}

// ListIn is the input of tasklist.list.
type ListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only lists of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" alias:"to" desc:"only lists of this store (default every attached store)"`
	// MetaFlags: --meta / --place / --sort meta.<key> (METADATA 3.7).
	work.MetaFlags
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// ListOut is the output of tasklist.list.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Notes are the metadata filter's lines (METADATA 3.7, 3.13).
	Notes []string `json:"notes,omitempty"`
	// tasks is each listed row's live task count, by id (human only:
	// the JSON keeps v0.1.17's record).
	tasks map[string]int
}

// Text prints one work.ListLine per list ("<id>  <title> [<status>]  (<n> tasks)"); "(no tasklists)" when none.
func (o ListOut) Text() string { return o.rowsText() + work.NotesText(o.Notes) }

// rowsText is the list lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no tasklists)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(work.ListLine(r.ID, r.Title, r.StatusStr, o.tasks[r.ID], r.Label))
	}
	return b.String()
}

// IDIn is the input of the operations that take one tasklist id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the tasklist id (tlt_…)"`
}

// DeleteIn is the input of tasklist.delete.
type DeleteIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"the tasklist id (tlt_…); leave out with --where"`
	// Where / DryRun are the bulk delete (DELETION 3.2, FA-LTASK-2): every
	// matching row in one bat_ batch, always asked (ops/internal/wheredel).
	Where  []string `json:"where,omitempty" flag:"where" desc:"bulk delete: every tasklist matching this filter (field=value, field:a,b, created_at<2026-06-01; repeatable, all must match)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --where: list what would be deleted, write nothing"`
	Reason string   `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
	// PurgeFlags: --purge / --confirm (engine binds them, P3-DEL-1).
	op.PurgeFlags
}

// EditIn is the input of tasklist.edit (only the flags given change).
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

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
	Store   string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only lists of this store (default every attached store)"`
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
		op.Define(op.Spec{Name: OpList, Examples: []op.Example{{Title: "list as JSON", CLI: "lore tasklist list --json", Want: `"data"`}}, Summary: "List tasklists", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.list),
		op.Define(op.Spec{Name: OpShow, Examples: []op.Example{{Title: "show as JSON", CLI: "lore tasklist show --id tlt_<id> --json", Want: `"data"`}}, Summary: "Show tasklist details", Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(work.ReadErrors), Confirm: expand.RevealConfirm}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Examples: []op.Example{{Title: "retitle", CLI: `lore tasklist edit --id tlt_<id> --title "Backlog"`, Want: "tlt_"}}, Summary: "Edit tasklist fields",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors, Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Examples: []op.Example{{Title: "preview a bulk delete", CLI: "lore tasklist delete --where 'created_at<2026-01-01' --dry-run", Want: "would"}}, Summary: "Delete a tasklist and its tasks, with their sub-tasks (one batch, to the trash)",
			// DQ6: the op asks itself, so the question names what goes with
			// the list; it always asks.
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors, Confirm: op.ConfirmRule{MayAsk: true},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Examples: []op.Example{{Title: "find tasklists", CLI: `lore tasklist search --query "inbox"`, Want: "tlt_"}}, Summary: "Full-text search across tasklist title + body + status",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.SearchErrors,
		}, o.search),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env work.Env }

// listID parses a tasklist id argument.
func listID(s string) (ids.AnyID, error) { return work.ParseID(Noun, ids.PrefixTaskList, s) }

// recordOf is the v0.1.17 JSON of a tasklist row.
func recordOf(lab work.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy, ProjectID: work.Field(r, work.FieldProject),
		Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody), StatusStr: work.Field(r, fieldStatus),
		TagIDs: work.TagIDs(r), Store: work.SourceOf(lab, src), Label: work.Label(lab, src),
	}
}

// addTitleBody reads the add's title and body: `add --title <t> [--body
// <b>]`, and v0.1.17's `add --title <t> "<body>"` (the argument is the
// body). A title is required; a body is optional, as on task add.
func addTitleBody(in AddIn) (title, body string, err error) {
	title = strings.TrimSpace(in.Title)
	if title == "" {
		return "", "", errs.New(errs.InvalidInput, "tasklist add: --title is required").
			WithHint(`lore tasklist add --title "Sprint 4"`)
	}
	if in.Body != "" && in.Text != "" {
		return "", "", errs.New(errs.InvalidInput, "tasklist add: give the body once, with --body").
			WithHint(`lore tasklist add --title "Sprint 4" --body "two weeks"`)
	}
	return title, in.Body + in.Text, nil
}

// add creates one tasklist.
func (o ops) add(ctx context.Context, in AddIn) (ShowOut, error) {
	title, body, err := addTitleBody(in)
	if err != nil {
		return ShowOut{}, err
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
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindAdd, Data: recordOf(work.LabelerOf(o.env), src, w.Row), Human: work.Success + " " + w.Row.ID.String() + "\n"}, nil
}

// list returns the tasklists of every attached store.
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
	all, notes, err := work.Narrow(ctx, o.env, all, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: work.SchemaVersion, Kind: KindList, Data: []Record{}, Notes: notes}
	for _, s := range all {
		if project == "" || work.Field(s.Row, work.FieldProject) == project {
			out.Data = append(out.Data, withMeta(ctx, o.env, recordOf(work.LabelerOf(o.env), s.Source, s.Row), s.Row))
		}
	}
	out.Count = len(out.Data)
	if out.tasks, err = work.TaskCounts(ctx, o.env, work.FieldTasklist); err != nil {
		return ListOut{}, err
	}
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
	n, err := work.CountTasks(ctx, o.env, work.FieldTasklist, id.String())
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.env, recordOf(work.LabelerOf(o.env), src, r), r), tasks: n}, nil
}

// withMeta is rec with the row's metadata (METADATA 3.17, MQ13: show and
// list --json carry it; sensitive values masked).
func withMeta(ctx context.Context, env metashow.Env, rec Record, r row.Row) Record {
	b := metashow.Row(ctx, env, r)
	rec.Meta, rec.MetaMasked = b.Meta, b.MetaMasked
	return rec
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
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return work.WriteOut{}, errs.New(errs.InvalidInput, "tasklist edit: nothing to change").WithHint("give at least one of --title, --body, --status")
	}
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" tasklist "+id.String()+" updated\n")
}

// delete tombstones the tasklist and every task in it, with their
// sub-tasks even in other lists (DQ6, TASK 6.12.1): one bat_ batch of
// kind delete through the relations' cascade, after a confirm that counts
// what goes with the list.
//
//	count list tasks + their sub-tasks in other lists → bulk.Ask → Begin →
//	engine Delete (tasks.tasklist_id and tasks.parent_id cascade)
func (o ops) delete(ctx context.Context, in DeleteIn) (work.WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	bulkForm, err := wheredel.Form(Noun, in.ID, in.Where, in.DryRun)
	if err != nil {
		return work.WriteOut{}, err
	}
	if bulkForm {
		res, werr := wheredel.Run(ctx, o.env, wheredel.Spec{Noun: Noun, Table: table, Aliases: map[string]string{whereStatus: fieldStatus}, Fence: func(fctx context.Context, tasks []wheredel.Located) (int, error) {
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
	id, err := listID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	inList, elsewhere, taken, err := o.listTasks(ctx, id.String())
	if err != nil {
		return work.WriteOut{}, err
	}
	question := "delete tasklist " + id.String() + " " + work.Quote(work.Field(cur, work.FieldTitle)) + "?"
	if inList+elsewhere > 0 {
		question = "deleting tasklist " + id.String() + " " + work.Quote(work.Field(cur, work.FieldTitle)) + " also deletes " +
			strconv.Itoa(inList) + " task(s)"
		if elsewhere > 0 {
			question += " and " + strconv.Itoa(elsewhere) + " sub-task(s) in other lists"
		}
		question += "; delete them all?"
	}
	if err = bulk.Ask(ctx, question); err != nil {
		return work.WriteOut{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return work.WriteOut{}, err
	}
	human := trashline.Moved(id.String(), id.String())
	if inList+elsewhere > 0 {
		human = trashline.MovedBatch("tasklist "+id.String()+" + "+strconv.Itoa(inList+elsewhere)+" tasks", batch.String())
	}
	// TASK 6.12 / FA-TASK-7: claimed tasks the cascade takes are fenced
	// (claim_epoch +1) inside the delete batch, before the tombstones.
	n, err := opstask.FenceClaimed(bctx, o.env, taken)
	if err != nil {
		return work.WriteOut{}, err
	}
	if n > 0 {
		human += "  fenced " + strconv.Itoa(n) + " claimed task(s): their holders' next write fails\n"
	}
	return work.Delete(bctx, o.env, Noun, table, id, human)
}

// listTasks counts the live tasks of list and their sub-tasks that sit in
// other lists (they go with their parent, 6.12.1), and returns every row
// the delete takes (for the claim fence).
func (o ops) listTasks(ctx context.Context, list string) (inList, elsewhere int, taken []work.Sourced, err error) {
	all, err := work.ListAll(ctx, o.env, schema.TableTasks, "")
	if err != nil {
		return 0, 0, nil, err
	}
	byID := map[string]work.Sourced{}
	var nodes []tasktree.Node
	var roots []string
	for _, s := range all {
		if s.Row.Deleted {
			continue
		}
		byID[s.Row.ID.String()] = s
		nodes = append(nodes, tasktree.Node{ID: s.Row.ID.String(), Parent: work.Field(s.Row, schema.FieldTaskParent)})
		if work.Field(s.Row, work.FieldTasklist) == list {
			roots = append(roots, s.Row.ID.String())
		}
	}
	t := tasktree.Build(nodes)
	gone := map[string]bool{}
	for _, r := range roots {
		gone[r] = true
		taken = append(taken, byID[r])
	}
	inList = len(roots)
	for _, r := range roots {
		for _, d := range t.Descendants(r) {
			if !gone[d] {
				gone[d] = true
				taken = append(taken, byID[d])
				elsewhere++
			}
		}
	}
	return inList, elsewhere, taken, nil
}

// search matches tasklists by title, body and status.
func (o ops) search(ctx context.Context, in SearchIn) (work.SearchEnvelope, error) {
	return work.SearchTable(ctx, o.env, table, KindSearch, in.Query, in.Limit, in.Project, in.Columns, in.Store, false, work.FieldTitle, work.FieldBody, fieldStatus)
}
