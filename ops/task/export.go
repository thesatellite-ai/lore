package task

// lore task export --format csv|json|markdown [--where …] [--tree] (TASK 19.2)
//
//	export ─► matchWhere (every task, or the --where set)
//	       ─► json: the exact shape task import --from json reads (round trip;
//	          the task id becomes the external id)
//	       ─► csv: the same columns plus status, parent, estimate, lead, times
//	       ─► markdown: "- [ ] title (id)" lines, nested by parent with --tree
//	Internal and sensitive fields are never exported (leak rules).

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
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
}

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
	hits, err := o.matchWhere(ctx, in.Where)
	if err != nil {
		return ExportOut{}, err
	}
	rows := make([]row.Row, len(hits))
	for i, s := range hits {
		rows[i] = s.Row
	}
	slices.SortStableFunc(rows, func(a, b row.Row) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	var content string
	switch format {
	case FormatCSV:
		content, err = exportCSV(rows)
	case FormatMarkdown:
		content = exportMarkdown(rows, in.Tree)
	default:
		content, err = exportJSON(rows)
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
		colDue: work.Field(r, fieldDue), colDeferUntil: work.Field(r, fieldDeferred),
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

func exportJSON(rows []row.Row) (string, error) {
	out := make([]map[string]string, len(rows))
	for i, r := range rows {
		out[i] = importShape(r)
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", errs.Wrap(errs.Internal, "task export: encode", err)
	}
	return string(raw) + "\n", nil
}

// csvColumns: the import columns (minus key) plus the read-only extras.
func csvColumns() []string {
	cols := slices.DeleteFunc(importColumns(), func(c string) bool { return c == colKey })
	return append(cols, colStatus, colParent, colEstimate, colLead, colCreated, colCompleted)
}

func exportCSV(rows []row.Row) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	cols := csvColumns()
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
func exportMarkdown(rows []row.Row, tree bool) string {
	var b strings.Builder
	line := func(r row.Row, depth int) {
		box := "[ ]"
		switch work.Field(r, work.FieldStatus) {
		case schema.TaskStatusDone:
			box = "[x]"
		case schema.TaskStatusCancelled:
			box = "[-]"
		}
		b.WriteString(strings.Repeat("  ", depth) + "- " + box + " " + work.Field(r, work.FieldTitle) + " (" + r.ID.String() + ")\n")
	}
	if !tree {
		for _, r := range rows {
			line(r, 0)
		}
		return b.String()
	}
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
	byRank := func(a, c row.Row) int {
		return cmp.Or(cmp.Compare(work.Field(a, schema.FieldTaskRank), work.Field(c, schema.FieldTaskRank)), cmp.Compare(a.ID.String(), c.ID.String()))
	}
	var walkTree func(r row.Row, depth int)
	walkTree = func(r row.Row, depth int) {
		line(r, depth)
		ks := kids[r.ID.String()]
		slices.SortStableFunc(ks, byRank)
		for _, k := range ks {
			walkTree(k, depth+1)
		}
	}
	for _, r := range roots {
		walkTree(r, 0)
	}
	return b.String()
}
