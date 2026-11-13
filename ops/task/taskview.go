package task

// Saved task views (TASK 16.2): `lore task view add|list|show|edit|delete`
// and `task list --view` / `task board --view`.
//
//	view add/edit ─► checkViewSpec (the --where parser, --sort keys,
//	                 --group-by) ─► filter_json {where, sort, group_by}
//	                 ─► one task_views row (tvw_, snake_case name per project)
//	list --view ─► findView ─► view's where/sort ANDed before the flags'
//	board --view ─► the same, plus the view's group_by unless --group-by

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names.
const (
	OpViewAdd    = "task.view.add"
	OpViewList   = "task.view.list"
	OpViewShow   = "task.view.show"
	OpViewEdit   = "task.view.edit"
	OpViewDelete = "task.view.delete"
)

const viewTable row.Table = schema.TableTaskViews

// viewNoun names the view in errors.
const viewNoun = "task view"

// ViewSpec is filter_json (16.2): the text expressions, never a parsed tree.
type ViewSpec struct {
	Where   string `json:"where,omitempty"`
	Sort    string `json:"sort,omitempty"`
	GroupBy string `json:"group_by,omitempty"`
	// Columns are task list --columns (16.2), used when the list gives none.
	Columns []string `json:"columns,omitempty"`
}

// sortList splits the stored sort ("urgency:desc,due_at") into --sort values.
func (s ViewSpec) sortList() []string {
	if s.Sort == "" {
		return nil
	}
	return strings.Split(s.Sort, ",")
}

// ViewAddIn is the input of task.view.add and task.view.edit.
type ViewAddIn struct {
	Name    string   `json:"name" arg:"0" desc:"the view name (snake_case) or, for edit, its tvw_ id"`
	Where   []string `json:"where,omitempty" flag:"where" desc:"the filter (the task list --where grammar; repeatable, all must match)"`
	Sort    []string `json:"sort,omitempty" flag:"sort" desc:"sort keys: <key>, <key>:asc or <key>:desc (repeatable; urgency allowed first)"`
	GroupBy string   `json:"group_by,omitempty" flag:"group-by" desc:"board grouping: status | assignee | priority | mission | repo | parent"`
	Columns []string `json:"columns,omitempty" flag:"columns" desc:"task list columns: id,title,estimate,tracked,… (comma-separated or repeated; the task list --columns names)"`
	Project string   `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	To      string   `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (default repo)"`
}

// ViewNameIn is the input of task.view.show.
type ViewNameIn struct {
	Name string `json:"name" arg:"0" desc:"the view name or tvw_ id"`
}

// ViewDeleteIn is the input of task.view.delete: the view and the delete
// reason every trash move takes (INTEG-TRASH-3, DELETION 2.1).
type ViewDeleteIn struct {
	Name   string `json:"name" arg:"0" desc:"the view name or tvw_ id"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// ViewListIn is the input of task.view.list.
type ViewListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only views of this project (prj_ id or name)"`
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// ViewOut is one view.
type ViewOut struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Project string   `json:"project_id"`
	Spec    ViewSpec `json:"filter"`
	Store   string   `json:"store,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

func (v ViewOut) line() string {
	parts := []string{v.ID, v.Name}
	if v.Spec.Where != "" {
		parts = append(parts, "where "+strconv.Quote(v.Spec.Where))
	}
	if v.Spec.Sort != "" {
		parts = append(parts, "sort "+v.Spec.Sort)
	}
	if v.Spec.GroupBy != "" {
		parts = append(parts, "group-by "+v.Spec.GroupBy)
	}
	return strings.Join(parts, "  ") + "\n"
}

// Text implements op.Texter.
func (v ViewOut) Text() string { return v.line() }

// ViewListOut is the output of task.view.list.
type ViewListOut struct {
	Views []ViewOut `json:"views"`
}

// Text implements op.Texter.
func (o ViewListOut) Text() string {
	if len(o.Views) == 0 {
		return "(no task views)\n"
	}
	var b strings.Builder
	for _, v := range o.Views {
		b.WriteString(v.line())
	}
	return b.String()
}

func taskViewOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpViewAdd, Summary: "Save a task list filter as a named view",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
			Examples: []op.Example{{Title: "an urgent-first view", CLI: "lore task view add --name my_urgent --where 'priority>=high' --sort urgency:desc", Want: "tvw_"}},
		}, o.viewAdd),
		op.Define(op.Spec{Name: OpViewList, Examples: []op.Example{{Title: "list as JSON", CLI: "lore task view list --json", Want: `"data"`}}, Summary: "List saved task views", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.viewList),
		op.Define(op.Spec{Name: OpViewShow, Examples: []op.Example{{Title: "one view", CLI: "lore task view show --name my_urgent", Want: "my_urgent"}}, Summary: "Show a saved task view", Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors}, o.viewShow),
		op.Define(op.Spec{Name: OpViewEdit, Examples: []op.Example{{Title: "change the sort", CLI: "lore task view edit --name my_urgent --sort due_at:asc", Want: "tvw_"}}, Summary: "Change a saved task view (only the flags you pass)", Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors}, o.viewEdit),
		op.Define(op.Spec{Name: OpViewDelete, Examples: []op.Example{{Title: "delete without a terminal", CLI: "lore task view delete --name my_urgent --yes", Want: "trash"}}, Summary: "Delete a saved task view", Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.DeleteErrors,
			Confirm: op.ConfirmRule{Always: true}}, o.viewDelete),
	}
}

// checkViewSpec refuses a view the list could not run (16.2: a bad
// expression is caught when saved, not when used).
func checkViewSpec(s ViewSpec) error {
	keys := taskKeys()
	if s.Where != "" {
		if _, _, err := listMatcher([]string{s.Where}, keys); err != nil {
			return err
		}
	}
	_, rest, err := urgencySort(s.sortList())
	if err != nil {
		return err
	}
	if _, err = listSort(rest, keys); err != nil {
		return err
	}
	if _, err = parseColumns(s.Columns, keys); err != nil {
		return err
	}
	if s.GroupBy != "" {
		return work.OneOf(viewNoun, "group-by", s.GroupBy, GroupBys())
	}
	return nil
}

func encodeSpec(s ViewSpec) (value.Value, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task view: encode", err)
	}
	return value.TextOf(string(raw))
}

// specOf decodes a view row's filter_json; old v0.1.17 free JSON that is
// not this shape reads as an empty spec plus an error naming the fix.
func specOf(r row.Row) (ViewSpec, error) {
	var s ViewSpec
	raw := work.Field(r, schema.FieldTaskViewFilter)
	if raw == "" {
		return s, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return ViewSpec{}, errs.New(errs.InvalidInput, "task view "+work.Field(r, schema.FieldTaskViewName)+": filter_json is not {where, sort, group_by, columns}").
			WithHint("lore task view edit --name " + r.ID.String() + " --where '…'")
	}
	return s, nil
}

func viewOutOf(src store.Source, r row.Row) (ViewOut, error) {
	s, err := specOf(r)
	return ViewOut{ID: r.ID.String(), Name: work.Field(r, schema.FieldTaskViewName), Project: work.Field(r, work.FieldProject),
		Spec: s, Store: work.SourceName(src), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy}, err
}

func (o ops) viewAdd(ctx context.Context, in ViewAddIn) (ViewOut, error) {
	name := strings.TrimSpace(in.Name)
	if !row.ValidName(name) {
		return ViewOut{}, errs.New(errs.InvalidInput, "task view name "+strconv.Quote(name)+" must be snake_case (a-z, 0-9, _), starting with a letter").WithHint("e.g. lore task view add --name my_urgent --where 'priority>=high'")
	}
	project, err := work.Project(ctx, o.env, viewNoun, in.Project)
	if err != nil {
		return ViewOut{}, err
	}
	if _, _, ferr := o.findView(ctx, name); ferr == nil {
		return ViewOut{}, errs.New(errs.InvalidInput, "task view "+strconv.Quote(name)+" exists").WithHint("lore task view edit --name " + name + " --where '…'")
	} else if !errs.Has(ferr, errs.NotFound) {
		return ViewOut{}, ferr
	}
	spec := ViewSpec{Where: strings.Join(in.Where, " "), Sort: strings.Join(in.Sort, ","), GroupBy: in.GroupBy, Columns: columnList(in.Columns)}
	if err = checkViewSpec(spec); err != nil {
		return ViewOut{}, err
	}
	fv, err := encodeSpec(spec)
	if err != nil {
		return ViewOut{}, err
	}
	nv, err := value.TextOf(name)
	if err != nil {
		return ViewOut{}, err
	}
	pv, err := value.RefOf(project.Any())
	if err != nil {
		return ViewOut{}, err
	}
	src, w, err := work.Create(ctx, o.env, in.To, viewTable, map[string]value.Value{
		work.FieldProject: pv, schema.FieldTaskViewName: nv, schema.FieldTaskViewFilter: fv,
	})
	if err != nil {
		return ViewOut{}, err
	}
	return viewOutOf(src, w.Row)
}

func (o ops) viewEdit(ctx context.Context, in ViewAddIn) (ViewOut, error) {
	src, cur, err := o.findView(ctx, in.Name)
	if err != nil {
		return ViewOut{}, err
	}
	spec, _ := specOf(cur) // a bad old value is replaced by the edit
	if len(in.Where) > 0 {
		spec.Where = strings.Join(in.Where, " ")
	}
	if len(in.Sort) > 0 {
		spec.Sort = strings.Join(in.Sort, ",")
	}
	if in.GroupBy != "" {
		spec.GroupBy = in.GroupBy
	}
	if len(in.Columns) > 0 {
		spec.Columns = columnList(in.Columns)
	}
	if len(in.Where) == 0 && len(in.Sort) == 0 && in.GroupBy == "" && len(in.Columns) == 0 {
		return ViewOut{}, errs.New(errs.InvalidInput, "task view edit: nothing to change").WithHint("give --where, --sort, --group-by or --columns")
	}
	if err = checkViewSpec(spec); err != nil {
		return ViewOut{}, err
	}
	fv, err := encodeSpec(spec)
	if err != nil {
		return ViewOut{}, err
	}
	w, err := o.env.Update(ctx, engine.Ref{Store: src, Table: viewTable, ID: cur.ID}, map[string]value.Value{schema.FieldTaskViewFilter: fv})
	if err != nil {
		return ViewOut{}, err
	}
	return viewOutOf(src, w.Row)
}

func (o ops) viewShow(ctx context.Context, in ViewNameIn) (ViewOut, error) {
	src, r, err := o.findView(ctx, in.Name)
	if err != nil {
		return ViewOut{}, err
	}
	return viewOutOf(src, r)
}

func (o ops) viewList(ctx context.Context, in ViewListIn) (ViewListOut, error) {
	project, err := work.ProjectFilter(ctx, o.env, viewNoun, in.Project)
	if err != nil {
		return ViewListOut{}, err
	}
	all, err := work.ListAll(ctx, o.env, viewTable, "")
	if err != nil {
		return ViewListOut{}, err
	}
	out := ViewListOut{Views: []ViewOut{}}
	for _, s := range all {
		if s.Row.Deleted || (project != "" && work.Field(s.Row, work.FieldProject) != project) {
			continue
		}
		v, verr := viewOutOf(s.Source, s.Row)
		if verr != nil {
			v.Spec.Where = "(unreadable filter_json: lore task view edit --name " + v.ID + ")"
		}
		out.Views = append(out.Views, v)
	}
	slices.SortFunc(out.Views, func(a, b ViewOut) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (o ops) viewDelete(ctx context.Context, in ViewDeleteIn) (work.WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // E_REASON_REQUIRED under delete.require_reason
	_, r, err := o.findView(ctx, in.Name)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Delete(ctx, o.env, viewNoun, viewTable, r.ID, trashline.Moved(r.ID.String()+" — task view "+work.Field(r, schema.FieldTaskViewName), r.ID.String()))
}

// findView resolves a view by tvw_ id or name.
func (o ops) findView(ctx context.Context, name string) (store.Source, row.Row, error) {
	if strings.HasPrefix(name, string(ids.PrefixTaskView)+"_") {
		id, err := work.ParseID(viewNoun, ids.PrefixTaskView, name)
		if err != nil {
			return "", row.Row{}, err
		}
		return work.Get(ctx, o.env, viewNoun, viewTable, id)
	}
	all, err := work.ListAll(ctx, o.env, viewTable, "")
	if err != nil {
		return "", row.Row{}, err
	}
	var hits []work.Sourced
	for _, s := range all {
		if !s.Row.Deleted && work.Field(s.Row, schema.FieldTaskViewName) == name {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return "", row.Row{}, errs.New(errs.NotFound, "task view "+strconv.Quote(name)+" not found").WithHint("lore task view list")
	case 1:
		return hits[0].Source, hits[0].Row, nil
	}
	return "", row.Row{}, errs.New(errs.AmbiguousID, "task view "+strconv.Quote(name)+" exists in more than one store or project").
		WithHint("name it by id: " + hits[0].Row.ID.String())
}

// applyView folds a --view into list flags: the view's where joins the
// flags' (all must match), its sort comes first unless --sort is given.
func (o ops) applyView(ctx context.Context, in ListIn) (ListIn, ViewSpec, error) {
	if in.View == "" {
		return in, ViewSpec{}, nil
	}
	_, r, err := o.findView(ctx, in.View)
	if err != nil {
		return in, ViewSpec{}, err
	}
	spec, err := specOf(r)
	if err != nil {
		return in, ViewSpec{}, err
	}
	if spec.Where != "" {
		in.Where = append([]string{spec.Where}, in.Where...)
	}
	if len(in.Sort) == 0 {
		in.Sort = spec.sortList()
	}
	if len(in.Columns) == 0 {
		in.Columns = spec.Columns
	}
	in.View = ""
	return in, spec, nil
}
