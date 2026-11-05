package task

// lore task export --format csv|json|markdown [--where …] [--tree] (TASK 19.2)
//
//	export ─► matchWhere (every task, or the --where set)
//	       ─► json: the exact shape task import --from json reads (round trip;
//	          the task id becomes the external id)
//	       ─► csv: the same columns plus status, parent, estimate, lead, times
//	       ─► markdown: "- [ ] title (id)" lines, nested by parent with --tree
//	--with-children: every descendant of the set joins it (json nests them
//	  under "children", markdown nests them, csv keeps the parent column)
//	--with-checklist: json "checklist": [{text, done}], markdown items
//	--internal: internal fields too, refused in a client repo (leak rules,
//	  fail closed); without it internal and sensitive fields never export

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/direction"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpExport is `lore task export`.
const OpExport = "task.export"

// Export formats (19.2).
const (
	FormatCSV      = "csv"
	FormatJSON     = "json"
	FormatMarkdown = "markdown"
)

// ExportFormats lists the formats.
func ExportFormats() []string { return []string{FormatCSV, FormatJSON, FormatMarkdown} }

// Extra CSV columns beyond the import shape.
const (
	colStatus    = "status"
	colParent    = "parent"
	colEstimate  = "estimate"
	colLead      = "lead"
	colCreated   = "created_at"
	colCompleted = "completed_at"
)

// ExportIn is the input of task.export.
type ExportIn struct {
	Format string   `json:"format,omitempty" flag:"format" desc:"csv | json | markdown (default json); json is the shape task import --from json reads"`
	Where  []string `json:"where,omitempty" flag:"where" desc:"only tasks matching this filter (the task list --where grammar; repeatable)"`
	Tree   bool     `json:"tree,omitempty" flag:"tree" desc:"markdown: nest sub-tasks under their parent"`
	// 19.2: the subtree, the checklist and internal fields on request.
	WithChildren  bool `json:"with_children,omitempty" flag:"with-children" desc:"add every sub-task of the exported tasks (json nests them under children)"`
	WithChecklist bool `json:"with_checklist,omitempty" flag:"with-checklist" desc:"json and markdown: add each task's checklist"`
	Internal      bool `json:"internal,omitempty" flag:"internal" desc:"add internal fields (claims, time entries, attempts); refused in a client repo"`
}

// Export keys beyond the import columns.
const (
	keyChildren  = "children"
	keyChecklist = "checklist"
)

// ExportOut is the output of task.export: the file content.
type ExportOut struct {
	Format  string `json:"format"`
	Count   int    `json:"count"`
	Content string `json:"content"`
}

// Text implements op.Texter: the content alone, ready to redirect.
func (o ExportOut) Text() string { return o.Content }

func exportOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpExport, Summary: "Export tasks as CSV, JSON (the import shape) or Markdown",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
			Examples: []op.Example{{Title: "export a mission as CSV", CLI: "lore task export --where 'status:todo' --format csv", Want: "title"}},
		}, o.export),
	}
}

func (o ops) export(ctx context.Context, in ExportIn) (ExportOut, error) {
	format := cmp.Or(in.Format, FormatJSON)
	if err := work.OneOf(Noun, "format", format, ExportFormats()); err != nil {
		return ExportOut{}, err
	}
	if in.Internal {
		if err := o.internalPlace(ctx); err != nil {
			return ExportOut{}, err
		}
	}
	hits, err := o.matchWhere(ctx, in.Where)
	if err != nil {
		return ExportOut{}, err
	}
	rows := make([]row.Row, len(hits))
	for i, s := range hits {
		rows[i] = s.Row
	}
	if in.WithChildren {
		all, aerr := o.matchWhere(ctx, nil)
		if aerr != nil {
			return ExportOut{}, aerr
		}
		allRows := make([]row.Row, len(all))
		for i, s := range all {
			allRows[i] = s.Row
		}
		rows = withDescendants(rows, allRows)
	}
	slices.SortStableFunc(rows, func(a, b row.Row) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	opt := exportOpt{children: in.WithChildren, checklist: in.WithChecklist, internal: in.Internal}
	var content string
	switch format {
	case FormatCSV:
		content, err = exportCSV(rows, opt)
	case FormatMarkdown:
		content = exportMarkdown(rows, in.Tree || in.WithChildren, in.WithChecklist)
	default:
		content, err = exportJSON(rows, opt)
	}
	if err != nil {
		return ExportOut{}, err
	}
	return ExportOut{Format: format, Count: len(rows), Content: content}, nil
}

// importShape is one task in task import's columns (import.go).
func importShape(r row.Row) map[string]string {
	m := map[string]string{
		colExtID: r.ID.String(), colTitle: work.Field(r, work.FieldTitle), colBody: work.Field(r, work.FieldBody),
		colPriority: work.Field(r, fieldPriority), colCommitment: work.Field(r, fieldCommitment),
		colDue: exportDate(r, fieldDue), colDeferUntil: exportDate(r, fieldDeferred),
		colTasklist: work.Field(r, work.FieldTasklist), colMission: work.Field(r, work.FieldMission),
		colPlan: work.Field(r, work.FieldPlan), colRole: work.Field(r, fieldRole),
	}
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// exportDate is a date field in the form task import (and --due) reads
// back: a calendar day (midnight UTC, TSQ11) as YYYY-MM-DD, a datetime as
// RFC 3339 UTC; "" when unset. The stored text ("…T00:00:00.000000000Z")
// is not a form the import accepts (OV53).
func exportDate(r row.Row, name string) string {
	t := work.Time(r, name)
	if t.IsZero() {
		return ""
	}
	t = t.UTC()
	if t.Equal(t.Truncate(24 * time.Hour)) {
		return t.Format(time.DateOnly)
	}
	return t.Format(time.RFC3339)
}

// exportOpt is the 19.2 extras of one export.
type exportOpt struct{ children, checklist, internal bool }

// internalPlace refuses --internal unless the repo store is internal
// (leak rules, 19.2); an Env that cannot classify stores fails closed.
func (o ops) internalPlace(ctx context.Context) error {
	class := direction.Client
	if c, ok := o.env.(pointer.Classifier); ok {
		var err error
		if class, err = c.StoreClass(ctx, store.SourceRepo); err != nil {
			return err
		}
	}
	if class == direction.Client {
		return errs.New(errs.InvalidInput, "task export --internal: this is a client repo, internal fields never leave an internal place (leak rules)").
			WithHint("run the export without --internal, or from the internal repo or org store")
	}
	return nil
}

// withDescendants is rows plus every descendant of them in all (by
// parent_id), each once.
func withDescendants(rows, all []row.Row) []row.Row {
	kids := map[string][]row.Row{}
	for _, r := range all {
		if p := work.Field(r, schema.FieldTaskParent); p != "" {
			kids[p] = append(kids[p], r)
		}
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.ID.String()] = true
	}
	out := slices.Clone(rows)
	for i := 0; i < len(out); i++ { // out grows while walked: breadth first
		for _, k := range kids[out[i].ID.String()] {
			if !seen[k.ID.String()] {
				seen[k.ID.String()] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// internalFields are the tasks fields marked Internal in the schema.
func internalFields() []string {
	var out []string
	for _, t := range schema.Builtin() {
		if t.Name != table {
			continue
		}
		for _, f := range t.Fields {
			if f.Internal {
				out = append(out, f.Name)
			}
		}
	}
	return out
}

// exportRecord is one task of the json export: the import columns, then
// the requested extras. With children, the rows whose parent is in the
// set nest under it instead of standing at the top level.
func exportRecord(r row.Row, kids map[string][]row.Row, opt exportOpt) (map[string]json.RawMessage, error) {
	rec := map[string]json.RawMessage{}
	put := func(k string, raw []byte, err error) error {
		if err != nil {
			return errs.Wrap(errs.Internal, "task export: encode "+k, err)
		}
		rec[k] = raw
		return nil
	}
	for k, v := range importShape(r) {
		raw, err := json.Marshal(v)
		if perr := put(k, raw, err); perr != nil {
			return nil, perr
		}
	}
	if opt.checklist {
		type item struct {
			Text string `json:"text"`
			Done bool   `json:"done"`
		}
		items := []item{}
		for _, it := range Checklist(r) {
			items = append(items, item{Text: it.Text, Done: it.Done})
		}
		raw, err := json.Marshal(items)
		if perr := put(keyChecklist, raw, err); perr != nil {
			return nil, perr
		}
	}
	if opt.internal {
		for _, f := range internalFields() {
			v := r.Fields[f]
			if v == nil {
				continue
			}
			raw, err := v.MarshalJSON()
			if perr := put(f, raw, err); perr != nil {
				return nil, perr
			}
		}
	}
	if opt.children {
		var cs []map[string]json.RawMessage
		for _, k := range kids[r.ID.String()] {
			c, err := exportRecord(k, kids, opt)
			if err != nil {
				return nil, err
			}
			cs = append(cs, c)
		}
		if len(cs) > 0 {
			raw, err := json.Marshal(cs)
			if perr := put(keyChildren, raw, err); perr != nil {
				return nil, perr
			}
		}
	}
	return rec, nil
}

func exportJSON(rows []row.Row, opt exportOpt) (string, error) {
	kids := map[string][]row.Row{}
	roots := rows
	if opt.children {
		roots, kids = splitRoots(rows)
	}
	out := make([]map[string]json.RawMessage, 0, len(roots))
	for _, r := range roots {
		rec, err := exportRecord(r, kids, opt)
		if err != nil {
			return "", err
		}
		out = append(out, rec)
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", errs.Wrap(errs.Internal, "task export: encode", err)
	}
	return string(raw) + "\n", nil
}

// splitRoots splits rows into the ones whose parent is not in the set and
// the children of each parent in it (siblings by rank, then id).
func splitRoots(rows []row.Row) ([]row.Row, map[string][]row.Row) {
	in := map[string]bool{}
	for _, r := range rows {
		in[r.ID.String()] = true
	}
	kids := map[string][]row.Row{}
	var roots []row.Row
	for _, r := range rows {
		if p := work.Field(r, schema.FieldTaskParent); p != "" && in[p] {
			kids[p] = append(kids[p], r)
			continue
		}
		roots = append(roots, r)
	}
	for _, ks := range kids {
		slices.SortStableFunc(ks, byRank)
	}
	return roots, kids
}

// byRank orders siblings by rank, then id.
func byRank(a, c row.Row) int {
	return cmp.Or(cmp.Compare(work.Field(a, schema.FieldTaskRank), work.Field(c, schema.FieldTaskRank)), cmp.Compare(a.ID.String(), c.ID.String()))
}

// csvColumns: the import columns (minus key) plus the read-only extras.
// --internal appends the internal fields (their JSON text).
func csvColumns(internal bool) []string {
	cols := slices.DeleteFunc(importColumns(), func(c string) bool { return c == colKey })
	cols = append(cols, colStatus, colParent, colEstimate, colLead, colCreated, colCompleted)
	if internal {
		cols = append(cols, internalFields()...)
	}
	return cols
}

func exportCSV(rows []row.Row, opt exportOpt) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	cols := csvColumns(opt.internal)
	if err := w.Write(cols); err != nil {
		return "", errs.Wrap(errs.Internal, "task export: csv", err)
	}
	for _, r := range rows {
		m := importShape(r)
		m[colStatus] = work.Field(r, work.FieldStatus)
		m[colParent] = work.Field(r, schema.FieldTaskParent)
		m[colEstimate] = work.Field(r, fieldEstimate)
		m[colLead] = work.Field(r, fieldAssigned)
		m[colCreated] = createdText(r)
		m[colCompleted] = work.Field(r, work.FieldCompleted)
		if opt.internal {
			for _, f := range internalFields() {
				if v := r.Fields[f]; v != nil {
					raw, err := v.MarshalJSON()
					if err != nil {
						return "", errs.Wrap(errs.Internal, "task export: encode "+f, err)
					}
					m[f] = string(raw)
				}
			}
		}
		rec := make([]string, len(cols))
		for i, c := range cols {
			rec[i] = m[c]
		}
		if err := w.Write(rec); err != nil {
			return "", errs.Wrap(errs.Internal, "task export: csv", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", errs.Wrap(errs.Internal, "task export: csv", err)
	}
	return buf.String(), nil
}

// createdText is the envelope created_at as RFC 3339 ("" when unset).
func createdText(r row.Row) string {
	if t := timeText(r.CreatedAt); len(t) > 0 {
		return t[0]
	}
	return ""
}

// exportMarkdown writes one checkbox line per task; --tree nests children
// under a parent in the set (siblings by rank, then id).
func exportMarkdown(rows []row.Row, tree, checklist bool) string {
	var b strings.Builder
	line := func(r row.Row, depth int) {
		mark := "[ ]"
		switch work.Field(r, work.FieldStatus) {
		case schema.TaskStatusDone:
			mark = "[x]"
		case schema.TaskStatusCancelled:
			mark = "[-]"
		}
		b.WriteString(strings.Repeat("  ", depth) + "- " + mark + " " + work.Field(r, work.FieldTitle) + " (" + r.ID.String() + ")\n")
		if checklist {
			for _, it := range Checklist(r) {
				b.WriteString(strings.Repeat("  ", depth+1) + "- " + box(it.Done) + " " + it.Text + "\n")
			}
		}
	}
	if !tree {
		for _, r := range rows {
			line(r, 0)
		}
		return b.String()
	}
	roots, kids := splitRoots(rows)
	var walkTree func(r row.Row, depth int)
	walkTree = func(r row.Row, depth int) {
		line(r, depth)
		for _, k := range kids[r.ID.String()] {
			walkTree(k, depth+1)
		}
	}
	for _, r := range roots {
		walkTree(r, 0)
	}
	return b.String()
}
