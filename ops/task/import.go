package task

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/estimate"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/checklist"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/fromfile"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpImport is `lore task import --from csv|json <file>`. XQ9: importing is
// a verb under the noun it creates (no top-level `lore import`); the CSV
// and JSON importers are in core, plugin sources (github, linear, jira),
// `lore resource import` and `lore meta import` are deferred (Q-R3-1).
const OpImport = "task.import"

// Import sources built into core (XQ9 `--from`).
const (
	FromCSV  = "csv"
	FromJSON = "json"
)

// Import columns: the task fields a file may set, by name. A file's own
// column names are renamed to these with --map.
const (
	colExtID      = "id"
	colTitle      = "title"
	colBody       = "body"
	colPriority   = "priority"
	colCommitment = "commitment"
	colDue        = "due"
	colDeferUntil = "defer_until"
	colTasklist   = "tasklist"
	colMission    = "mission"
	colPlan       = "plan"
	colRole       = "role"
	colKey        = "key"
	// The titles of the tasklist, mission and plan, beside their ids: an
	// import matches by name when the id is not in the target (G-DECIDE-5).
	colTasklistName = "tasklist_name"
	colMissionName  = "mission_name"
	colPlanName     = "plan_name"
)

// refColumn is one reference an import file may carry: the id column, its
// name column, the task field and the table the id names.
type refColumn struct {
	col, nameCol, field string
	tbl                 row.Table
}

// refColumns lists the tasklist, mission and plan references.
func refColumns() []refColumn {
	return []refColumn{
		{colTasklist, colTasklistName, work.FieldTasklist, schema.TableTasklists},
		{colMission, colMissionName, work.FieldMission, schema.TableMissions},
		{colPlan, colPlanName, work.FieldPlan, schema.TablePlans},
	}
}

// Import words.
const (
	mapSep      = "="
	mapListSep  = ","
	extIDRowSep = ":" // ext_id of a row without an id column: <file>:<row>
	// extIDKidSep joins a child's position to its parent's ext_id when the
	// child has no id: <parent ext_id>/<n> (stable on re-import).
	extIDKidSep = "/"
	// maxImportBytes bounds the file an import reads (a task file far
	// over this is a mistake, not a backlog).
	maxImportBytes = 16 << 20
	// utf8BOM is the byte order mark some spreadsheet tools put before
	// a CSV's first header.
	utf8BOM = "\uFEFF"
)

// importColumns lists every column an import reads, in a fixed order.
func importColumns() []string {
	return []string{colExtID, colTitle, colBody, colPriority, colCommitment, colDue, colDeferUntil, colTasklist, colMission, colPlan, colRole, colKey,
		colTasklistName, colMissionName, colPlanName, colStatus, colEstimate, colParent}
}

// exportOnlyColumns are the CSV export columns an import reads past
// (G-TASK-3): a lead is an actor of the exporting org and the two
// timestamps are the exporter's record, so they are not set on the new
// row; each one present is named once in a W_IMPORT_IGNORED_COLUMN line.
func exportOnlyColumns() []string { return []string{colLead, colCreated, colCompleted} }

// WarnImportIgnoredColumn names an export-only column an import read past.
const WarnImportIgnoredColumn = "W_IMPORT_IGNORED_COLUMN"

// ImportIn is the input of task.import.
type ImportIn struct {
	File string   `json:"file" arg:"0" desc:"the file to import"`
	From string   `json:"from,omitempty" flag:"from" desc:"csv | json (default: from the file extension)"`
	Map  []string `json:"map,omitempty" flag:"map" desc:"rename columns: <file column>=<task field>[,…] (fields: id title body priority commitment due defer_until tasklist mission plan role key tasklist_name mission_name plan_name status estimate parent)"`
	// MapStatus renames the file's status values (TASK 19.1, G-TASK-3).
	MapStatus []string `json:"map_status,omitempty" flag:"map-status" desc:"rename status values: <file value>=<status>[,…], e.g. 'In Review=in_progress' (statuses: todo in_progress done cancelled blocked)"`
	DryRun    bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would be created; write nothing"`
	To        string   `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
}

// ImportRow is one task an import created (or would create).
type ImportRow struct {
	ID    string `json:"id,omitempty"`
	ExtID string `json:"ext_id"`
	Title string `json:"title"`
	// ParentExtID is set for a task read from a parent's "children".
	ParentExtID string `json:"parent_ext_id,omitempty"`
	// Checklist is how many checklist items the task got.
	Checklist int `json:"checklist,omitempty"`
	// Existing is set when a live task in the target already carries this
	// (source, external id) (G-TASK-3): nothing is added for it, and
	// Differs names the file's fields whose value is not the task's (not
	// written; lore task edit changes them).
	Existing bool     `json:"existing,omitempty"`
	Differs  []string `json:"differs,omitempty"`
}

// ImportOut is the output of task.import.
type ImportOut struct {
	Batch  string      `json:"batch,omitempty"`
	Source string      `json:"source"`
	DryRun bool        `json:"dry_run,omitempty"`
	Count  int         `json:"count"`
	Rows   []ImportRow `json:"rows"`
	// Warnings are the W_IMPORT_DROPPED_REF lines, one per reference
	// dropped because its row is not in the target (PC-TASK-1).
	Warnings []Warning `json:"warnings,omitempty"`
}

// WarnImportDroppedRef names a tasklist, mission or plan id an imported
// row pointed at that the target repo does not hold (an export from
// another repo); the reference is dropped, never the whole file
// (PC-TASK-1). W_ codes live beside their writer, like engine's.
const WarnImportDroppedRef = "W_IMPORT_DROPPED_REF"

// Text implements op.Texter.
func (o ImportOut) Text() string {
	if o.DryRun {
		var b strings.Builder
		// B3-24: the ids listed are the file's; the import writes each task
		// under a new id (the file id is kept as its import origin)
		b.WriteString("would import " + plural.Count(o.Count-o.existing(), "task") + " from " + o.Source + " as new tasks with new ids (dry run, nothing written); the ids below are the file's\n")
		for _, r := range o.Rows {
			b.WriteString("  " + r.ExtID + "  " + r.Title + r.existingNote() + "\n")
		}
		o.warnLines(&b)
		return b.String()
	}
	var b strings.Builder
	if n := o.Count - o.existing(); n > 0 || o.existing() == 0 {
		b.WriteString(work.Success + " " + plural.Count(n, "task") + " imported from " + o.Source + " (batch " + o.Batch + "; `lore task list` shows their new ids)\n")
	}
	if e := o.existing(); e > 0 {
		b.WriteString("  " + strconv.Itoa(e) + " already imported, nothing added for them:\n")
		for _, r := range o.Rows {
			if r.Existing {
				b.WriteString("    " + r.ExtID + "  " + r.Title + r.existingNote() + "\n")
			}
		}
	}
	o.warnLines(&b)
	return b.String()
}

// existing counts the rows already imported (G-TASK-3).
func (o ImportOut) existing() int {
	n := 0
	for _, r := range o.Rows {
		if r.Existing {
			n++
		}
	}
	return n
}

// existingNote is the line suffix of an already imported row.
func (r ImportRow) existingNote() string {
	if !r.Existing {
		return ""
	}
	if len(r.Differs) == 0 {
		return "  (already imported as " + r.ID + ")"
	}
	return "  (already imported as " + r.ID + "; differs, not written: " + strings.Join(r.Differs, ", ") + ")"
}

// warnLines writes one "  W_…: message" line per warning.
func (o ImportOut) warnLines(b *strings.Builder) {
	for _, w := range o.Warnings {
		b.WriteString("  " + w.Code + ": " + w.Message + "\n")
	}
}

// ImportOps returns the task import operation bound to env, for
// op.NewRegistry. It is apart from Ops so the importer stays one file.
func ImportOps(env work.Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpImport, Summary: "Import tasks from a CSV or JSON file (one batch)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.NeedsConfirm),
			// XQ2: the preview asks above batch.preview_rows, after
			// counting the rows; MayAsk gives the command --yes.
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "import a CSV", CLI: "lore task import --from csv --file tasks.csv --yes", Want: "imported"}},
		}, o.importTasks),
	}
}

// importRecord is one parsed task of the file. Records are in pre-order:
// a parent always comes before its children (task export --with-children
// nests them under "children"), so the parent's id exists when a child is
// added.
type importRecord struct {
	extID string
	cols  map[string]string
	// parent is the index of the parent record, -1 for a top-level task.
	parent int
	// checklist is the export's [{text, done}] (--with-checklist).
	checklist []importItem
	// line is the record's first file line in a CSV (header = 1); 0 in JSON.
	line int
}

// problem is a record check's refusal as the problem of its place in the
// file (its CSV line, else its external id), so the import lists every bad
// record at once; ok is false for an error that is not a lore refusal.
func (r importRecord) problem(err error) (fromfile.Problem, bool) {
	var e *errs.Error
	if !errors.As(err, &e) {
		return fromfile.Problem{}, false
	}
	at := r.extID
	if r.line > 0 {
		at = fromfile.Line(r.line)
	}
	reason := strings.TrimPrefix(strings.TrimPrefix(e.Message, "task import: "), r.extID+" ")
	return fromfile.Problem{At: at, Reason: reason, Fix: e.Hint, Code: e.Code}, true
}

// importItem is one checklist item as task export --with-checklist writes it.
type importItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// importTasks reads the file, checks every record, previews (XQ2), then
// creates one task per record in one batch of kind import (XQ1), each with
// `_o.import {source, ext_id}` (PROVENANCE 5.1; PROV9 finds duplicates by
// it). Every record is checked before the first write, so a bad row writes
// nothing.
func (o ops) importTasks(ctx context.Context, in ImportIn) (ImportOut, error) {
	from, err := importSource(in.From, in.File)
	if err != nil {
		return ImportOut{}, err
	}
	rename, err := importMap(in.Map)
	if err != nil {
		return ImportOut{}, err
	}
	raw, err := readImportFile(in.File)
	if err != nil {
		return ImportOut{}, err
	}
	recs, ignored, err := parseImport(from, in.File, raw, rename)
	if err != nil {
		return ImportOut{}, err
	}
	statusMap, err := importStatusMap(in.MapStatus)
	if err != nil {
		return ImportOut{}, err
	}
	for _, r := range recs {
		if to, ok := statusMap[strings.TrimSpace(r.cols[colStatus])]; ok {
			r.cols[colStatus] = to
		}
	}
	if err = checkStatusTree(recs); err != nil {
		return ImportOut{}, err
	}
	out := ImportOut{Source: from, DryRun: in.DryRun, Rows: make([]ImportRow, 0, len(recs))}
	for _, c := range ignored {
		out.Warnings = append(out.Warnings, Warning{Code: WarnImportIgnoredColumn, Field: c,
			Message: "column " + strconv.Quote(c) + " is the exporter's record; not set on the imported tasks"})
	}
	items := make([]bulk.Item, 0, len(recs))
	var bad []fromfile.Problem
	for _, r := range recs {
		if err = checkRecord(r); err != nil {
			p, ok := r.problem(err)
			if !ok {
				return ImportOut{}, err
			}
			bad = append(bad, p)
			continue
		}
		ir := ImportRow{ExtID: r.extID, Title: strings.TrimSpace(r.cols[colTitle]), Checklist: len(r.checklist)}
		if r.parent >= 0 {
			ir.ParentExtID = recs[r.parent].extID
		}
		out.Rows = append(out.Rows, ir)
		items = append(items, bulk.Item{ID: r.extID, Label: strings.TrimSpace(r.cols[colTitle])})
	}
	// bad is refused after the estimates are checked too: one refusal
	// lists every bad record
	out.Count = len(recs)
	target := store.Source(in.To)
	if target == "" {
		target = store.SourceRepo
	}
	// G-TASK-3: every estimate is checked in the target's unit before the
	// first write, so a bad one never leaves a half-written import.
	unit, err := o.estimateUnit(ctx, target)
	if err != nil {
		return ImportOut{}, err
	}
	for _, r := range recs {
		if raw := r.cols[colEstimate]; raw != "" {
			if _, perr := estimate.Parse(raw, unit); perr != nil {
				p, ok := r.problem(perr)
				if !ok {
					return ImportOut{}, errors.Join(perr, errs.New(errs.InvalidInput, "task import: in "+r.extID))
				}
				bad = append(bad, p)
			}
		}
	}
	if e := fromfile.Refuse("task import --file", in.File, bad, "lore task import --file "+in.File+" --dry-run"); e != nil {
		return ImportOut{}, e
	}
	// G-TASK-3 (TSQ8 dedupe by external id): a record whose (source,
	// external id) a live task in the target already carries adds nothing.
	known, err := o.importedIn(ctx, target)
	if err != nil {
		return ImportOut{}, err
	}
	for i, r := range recs {
		if s, ok := known[origin.Import{Source: from, ExtID: r.extID}.Key()]; ok {
			out.Rows[i].ID, out.Rows[i].Existing = s.Row.ID.String(), true
			out.Rows[i].Differs = importDiffers(r, s.Row)
		}
	}
	// PC-TASK-1: a tasklist / mission / plan id the target does not hold
	// (an export from another repo) is dropped with a warning and a note.
	// G-DECIDE-5: an id the target does not hold falls back to the
	// export's name column before it is dropped.
	dropped, err := o.resolveRefs(ctx, recs, target)
	if err != nil {
		return ImportOut{}, err
	}
	// G-TASK-3: a parent column naming no record of the file must be a
	// task the target holds; any other value is dropped like a missing
	// tasklist (PC-TASK-1), never a half-written import.
	for i, r := range recs {
		p := strings.TrimSpace(r.cols[colParent])
		if p == "" || r.parent >= 0 {
			continue
		}
		if id, perr := ids.ParseAny(p); perr == nil {
			src, cur, gerr := o.env.Get(ctx, engine.Ref{Table: table, ID: id})
			if gerr == nil && src == target && !cur.Deleted {
				continue
			}
			if gerr != nil && !errs.Has(gerr, errs.NotFound) && !errs.Has(gerr, errs.InTrash) {
				return ImportOut{}, gerr
			}
		}
		delete(r.cols, colParent)
		dropped[i] = append(dropped[i], droppedRef{field: fieldParent, id: p})
	}
	for i, ds := range dropped {
		for _, d := range ds {
			out.Warnings = append(out.Warnings, Warning{Code: WarnImportDroppedRef, Field: d.field,
				Message: recs[i].extID + ": dropped " + d.field + " " + d.text()})
		}
	}
	if in.DryRun || len(recs) == out.existing() {
		return out, nil
	}
	if err = bulk.Preview(ctx, o.env, target, "task import", items); err != nil {
		return ImportOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindImport)
	if err != nil {
		return ImportOut{}, err
	}
	out.Batch = bid.String()
	project, err := work.Project(bctx, o.env, Noun, "")
	if err != nil {
		return ImportOut{}, err
	}
	// D-214: the records outside any task tree (most files) are written as
	// ONE staged batch: one store commit for the default list, every task,
	// its checklist, first status and dropped-ref note (2,000 rows: one
	// live commit, not 2,000, each re-reading a growing ref). A record of a
	// tree reads its parent back (rank, inheritance, width), so the batch
	// so far is committed first and the record is written on its own, as
	// before; file order is kept either way.
	tree := inTree(recs)
	sctx := engine.Staging(store.WithCommitTitle(bctx, "task import: "+strconv.Itoa(len(recs)-out.existing())+" tasks from "+from))
	defer func() { engine.Discard(sctx) }() // no-op once committed
	flush := func() error {
		_, ferr := engine.Commit(sctx)
		sctx = engine.Staging(store.WithCommitTitle(bctx, "task import"))
		return ferr
	}
	// The default list is created (if needed) outside the import slot: it
	// is not an imported row and must not carry _o.import.
	defList, _, err := o.defaultTasklist(sctx, in.To, project)
	if err != nil {
		return ImportOut{}, err
	}
	stagedKeys := map[string]string{} // keyOfRecord -> the id this batch gave it
	statusSet := make([]bool, len(recs))
	for i, r := range recs {
		if out.Rows[i].Existing {
			continue
		}
		wctx := sctx
		if tree[i] {
			if err = flush(); err != nil {
				return ImportOut{}, err
			}
			wctx = bctx
		} else if k := keyOfRecord(r); k != "" && stagedKeys[k] != "" {
			out.Rows[i].ID = stagedKeys[k] // add's --key rule: already added by this batch
			continue
		}
		// XQ9 / PROVENANCE 5.1: the row's _o.import names the importer
		// kind and the external id (no ext_ source record for a file).
		rctx := engine.WithImport(wctx, origin.Import{Source: from, ExtID: r.extID})
		add := AddIn{Title: r.cols[colTitle], Body: r.cols[colBody], Priority: r.cols[colPriority], Commitment: r.cols[colCommitment],
			Due: r.cols[colDue], DeferUntil: r.cols[colDeferUntil], Tasklist: orDefault(r.cols[colTasklist], defList),
			Mission: r.cols[colMission], Plan: r.cols[colPlan], Role: r.cols[colRole], Key: r.cols[colKey], To: in.To,
			Estimate: r.cols[colEstimate], Parent: r.cols[colParent]}
		if r.parent >= 0 {
			// TASK 6.5: a child without its own tasklist inherits the
			// parent's instead of the import default.
			add.Parent, add.Tasklist = out.Rows[r.parent].ID, r.cols[colTasklist]
		}
		if !tree[i] {
			add.status, statusSet[i] = r.cols[colStatus], true
			if len(r.checklist) > 0 {
				if add.checklist, err = o.importedItems(rctx, r.checklist); err != nil {
					return ImportOut{}, err
				}
			}
		}
		got, aerr := o.add(rctx, add)
		if aerr == nil && tree[i] && len(r.checklist) > 0 {
			aerr = o.importChecklist(rctx, got.Data.ID, r.checklist)
		}
		if aerr != nil {
			return ImportOut{}, errors.Join(aerr, errs.New(errs.InvalidInput, "task import: stopped at "+r.extID+" after "+
				strconv.Itoa(i)+" of "+strconv.Itoa(len(recs))+" tasks; the ones written are in batch "+out.Batch))
		}
		out.Rows[i].ID = got.Data.ID
		if k := keyOfRecord(r); k != "" && !tree[i] {
			stagedKeys[k] = got.Data.ID
		}
		for _, d := range dropped[i] {
			// PC-TASK-1: the row says what it lost, outside the import
			// slot (the note is not an imported row).
			if tree[i] {
				err = o.addNote(bctx, got.Data.ID, d.note())
			} else {
				err = o.stagedNote(sctx, store.Source(got.Store), got.Data.ID, d.note())
			}
			if err != nil {
				return ImportOut{}, err
			}
		}
	}
	if err = flush(); err != nil {
		return ImportOut{}, err
	}
	// G-TASK-3: a status other than todo is set after every task exists,
	// children before parents (pre-order reversed), so a done parent finds
	// its sub-tasks already closed (checkStatusTree refused the rest).
	for i := len(recs) - 1; i >= 0; i-- {
		st := recs[i].cols[colStatus]
		if out.Rows[i].Existing || statusSet[i] || st == "" || st == schema.TaskStatusTodo {
			continue
		}
		rctx := engine.WithImport(bctx, origin.Import{Source: from, ExtID: recs[i].extID})
		if _, err = o.edit(rctx, EditIn{ID: out.Rows[i].ID, Status: st}); err != nil {
			return ImportOut{}, errors.Join(err, errs.New(errs.InvalidInput, "task import: setting status "+st+" on "+recs[i].extID+
				" failed; the tasks written are in batch "+out.Batch))
		}
	}
	return out, nil
}

// importedIn maps each import key (source, external id) that a live task
// of target carries to that task (G-TASK-3; the oldest wins when PROV9
// duplicates exist, as lore doctor keeps the oldest).
func (o ops) importedIn(ctx context.Context, target store.Source) (map[origin.ImportKey]work.Sourced, error) {
	rows, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return nil, err
	}
	out := map[origin.ImportKey]work.Sourced{}
	for _, s := range rows {
		if s.Source != target {
			continue
		}
		k, ok := importKey(s.Row)
		if !ok {
			continue
		}
		if prev, seen := out[k]; seen && prev.Row.ID.String() < s.Row.ID.String() {
			continue
		}
		out[k] = s
	}
	return out, nil
}

// importDiffers names the file columns of r whose value is not the
// existing task's (G-TASK-3: reported, never written).
func importDiffers(r importRecord, cur row.Row) []string {
	fields := []struct{ col, field string }{
		{colTitle, work.FieldTitle}, {colBody, work.FieldBody}, {colPriority, fieldPriority},
		{colCommitment, fieldCommitment}, {colStatus, work.FieldStatus}, {colEstimate, fieldEstimate},
	}
	var out []string
	for _, f := range fields {
		v, ok := r.cols[f.col]
		if !ok {
			continue
		}
		if strings.TrimSpace(v) != work.Field(cur, f.field) {
			out = append(out, f.col)
		}
	}
	return out
}

// checkStatusTree refuses a file whose done or cancelled task has an open
// sub-task in the same file (G-TASK-3): the import could not close the
// parent (E_OPEN_CHILDREN) and would stop half written.
func checkStatusTree(recs []importRecord) error {
	closed := func(st string) bool { return st == schema.TaskStatusDone || st == schema.TaskStatusCancelled }
	for _, r := range recs {
		if r.parent < 0 || closed(r.cols[colStatus]) {
			continue
		}
		if p := recs[r.parent]; closed(p.cols[colStatus]) {
			return errs.New(errs.InvalidInput, "task import: "+p.extID+" is "+p.cols[colStatus]+" but its sub-task "+r.extID+" is open").
				WithHint("close the sub-task in the file too (status done or cancelled), or leave the parent open")
		}
	}
	return nil
}

// droppedRef is one reference an import dropped (PC-TASK-1).
type droppedRef struct {
	field string // the task field: tasklist_id, mission_id, plan_id
	id    string // the file's id ("" when the file named it only)
	// why is the name part of the reason (G-DECIDE-5): "" when the file
	// carried no name, else no row or several rows of that name.
	why string
}

// note is the comment the imported task gets for d (one line).
func (d droppedRef) note() string {
	return "import: dropped " + d.field + " " + d.text()
}

// text is the reference and the reason, after the field name.
func (d droppedRef) text() string {
	ref := d.id
	if ref == "" {
		ref = "(no id)"
	}
	reason := "not in this repo"
	if d.why != "" {
		reason += "; " + d.why
	}
	return ref + " (" + reason + ")"
}

// resolveRefs settles every tasklist / mission / plan reference of the
// records against the target (G-DECIDE-5, PC-TASK-1): an id that names a
// row in the attached stores (even in the trash, so task add's own checks
// apply) is kept; otherwise the name column picks the one live row of
// that title in this project in the target store; otherwise the reference
// is dropped and returned per record. A malformed id with no name is left
// for task add to refuse. The name columns never reach task add. The file
// creates no tasklists, missions or plans, so the target is the only
// place a reference can resolve.
func (o ops) resolveRefs(ctx context.Context, recs []importRecord, target store.Source) (map[int][]droppedRef, error) {
	byName := map[row.Table]map[string][]string{} // table -> title -> ids, loaded on first use
	named := func(tbl row.Table, name string) ([]string, error) {
		m, ok := byName[tbl]
		if !ok {
			var err error
			if m, err = o.titlesIn(ctx, target, tbl); err != nil {
				return nil, err
			}
			byName[tbl] = m
		}
		return m[name], nil
	}
	out := map[int][]droppedRef{}
	for i, r := range recs {
		for _, c := range refColumns() {
			raw, name := strings.TrimSpace(r.cols[c.col]), strings.TrimSpace(r.cols[c.nameCol])
			delete(r.cols, c.nameCol)
			if raw == "" && name == "" {
				continue
			}
			if raw != "" {
				id, perr := ids.ParseAny(raw)
				if perr != nil && name == "" {
					continue
				}
				if perr == nil {
					_, _, gerr := o.env.Get(ctx, engine.Ref{Table: c.tbl, ID: id})
					if gerr == nil {
						continue
					}
					if !errs.Has(gerr, errs.NotFound) {
						return nil, gerr
					}
				}
			}
			d := droppedRef{field: c.field, id: raw}
			if name != "" {
				hits, err := named(c.tbl, name)
				if err != nil {
					return nil, err
				}
				if len(hits) == 1 {
					r.cols[c.col] = hits[0]
					continue
				}
				if len(hits) == 0 && c.tbl == schema.TableTasklists && name == DefaultTasklistTitle {
					// The other repo's default list: the task lands in this
					// repo's default list (created by the import), nothing lost.
					delete(r.cols, c.col)
					continue
				}
				d.why = refNoun(c.tbl) + " named " + strconv.Quote(name)
				if len(hits) == 0 {
					d.why = "no " + d.why
				} else {
					d.why = strconv.Itoa(len(hits)) + " " + refNoun(c.tbl) + "s named " + strconv.Quote(name)
				}
			}
			delete(r.cols, c.col)
			out[i] = append(out[i], d)
		}
	}
	return out, nil
}

// refNoun is the word for a reference table in a drop reason.
func refNoun(tbl row.Table) string {
	switch tbl {
	case schema.TableTasklists:
		return "tasklist"
	case schema.TableMissions:
		return "mission"
	default:
		return "plan"
	}
}

// titlesIn maps title -> ids of the live rows of tbl in target that
// belong to this project (G-DECIDE-5 name matching).
func (o ops) titlesIn(ctx context.Context, target store.Source, tbl row.Table) (map[string][]string, error) {
	project, err := work.Project(ctx, o.env, Noun, "")
	if err != nil {
		return nil, err
	}
	rows, err := o.env.List(ctx, target, tbl)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	m := map[string][]string{}
	for _, r := range rows {
		if r.Deleted || work.Field(r, work.FieldProject) != project.String() {
			continue
		}
		t := strings.TrimSpace(work.Field(r, work.FieldTitle))
		m[t] = append(m[t], r.ID.String())
	}
	return m, nil
}

// importChecklist writes a record's checklist onto the task just added
// (TASK 7.3 items, ticked ones stamped by the importer now).
func (o ops) importChecklist(ctx context.Context, id string, items []importItem) error {
	h, err := o.getChecklist(ctx, id)
	if err != nil {
		return err
	}
	texts, done := make([]string, len(items)), make([]bool, len(items))
	for i, it := range items {
		texts[i], done[i] = it.Text, it.Done
	}
	if _, err = o.appendItems(ctx, &h, texts, done); err != nil {
		return err
	}
	_, err = o.saveChecklist(ctx, h, nil, "")
	return err
}

// importedItems is a record's checklist as the items checklist add would
// append to an empty list (TASK 7.3), for a task created with them in
// one staged batch (D-214).
func (o ops) importedItems(ctx context.Context, items []importItem) ([]checklist.Item, error) {
	texts, done := make([]string, len(items)), make([]bool, len(items))
	for i, it := range items {
		texts[i], done[i] = it.Text, it.Done
	}
	var h clHit
	if _, err := o.appendItems(ctx, &h, texts, done); err != nil {
		return nil, err
	}
	return h.list, nil
}

// importSource settles --from: given, or the file's extension.
func importSource(from, file string) (string, error) {
	if from == "" {
		from = strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
	}
	switch from {
	case FromCSV, FromJSON:
		return from, nil
	}
	return "", errs.New(errs.InvalidInput, "task import: --from "+strconv.Quote(from)+" is not a built-in source: use csv or json").
		WithHint("plugin sources (github, linear, jira) are not in this release")
}

// importStatusMap parses --map-status into file value → status; every
// target must be a task status (G-TASK-3).
func importStatusMap(items []string) (map[string]string, error) {
	out := map[string]string{}
	for _, item := range items {
		for _, pair := range strings.Split(item, ",") {
			from, to, ok := strings.Cut(pair, "=")
			from, to = strings.TrimSpace(from), strings.TrimSpace(to)
			if !ok || from == "" {
				return nil, errs.New(errs.InvalidInput, "task import: --map-status "+strconv.Quote(pair)+" is not <file value>=<status>")
			}
			if err := work.OneOf(Noun, "map-status", to, schema.TaskStatuses()); err != nil {
				return nil, err
			}
			out[from] = to
		}
	}
	return out, nil
}

// importMap parses --map into file column → task field.
func importMap(items []string) (map[string]string, error) {
	out := map[string]string{}
	for _, item := range items {
		for _, pair := range strings.Split(item, mapListSep) {
			if strings.TrimSpace(pair) == "" {
				continue
			}
			src, dst, ok := strings.Cut(pair, mapSep)
			src, dst = strings.TrimSpace(src), strings.TrimSpace(dst)
			if !ok || src == "" || !slices.Contains(importColumns(), dst) {
				return nil, errs.New(errs.InvalidInput, "task import: --map "+strconv.Quote(pair)+": want <file column>=<field>, field one of "+
					strings.Join(importColumns(), ", "))
			}
			out[src] = dst
		}
	}
	return out, nil
}

// readImportFile reads at most maxImportBytes of path.
func readImportFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errs.New(errs.InvalidInput, "task import: name the file to import")
	}
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "task import: open "+path, err).WithHint("check the path: it must name a readable CSV or JSON file")
	}
	defer func() { _ = fh.Close() }() // read only
	b, err := io.ReadAll(io.LimitReader(fh, maxImportBytes+1))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "task import: read "+path, err)
	}
	if len(b) > maxImportBytes {
		return nil, errs.New(errs.InvalidInput, "task import: "+path+" is over "+strconv.Itoa(maxImportBytes)+" bytes")
	}
	return b, nil
}

// parseImport turns the file into records with renamed columns. A record
// without an id column gets ext_id "<file>:<row>" (row 1 = the first
// record), so importing the same file again names the same external ids.
func parseImport(from, path string, raw []byte, rename map[string]string) ([]importRecord, []string, error) {
	base := filepath.Base(path)
	var rows []jsonRow
	if from == FromCSV {
		flat, lines, err := csvRows(path, raw)
		if err != nil {
			return nil, nil, err
		}
		for i, m := range flat {
			rows = append(rows, jsonRow{cols: m, parent: -1, line: lines[i]})
		}
	} else {
		var objs []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &objs); err != nil {
			return nil, nil, errs.Wrap(errs.InvalidInput, "task import: the JSON must be an array of objects", err)
		}
		var err error
		if rows, err = jsonRows(objs, -1, nil); err != nil {
			return nil, nil, err
		}
	}
	var ignored []string
	out := make([]importRecord, 0, len(rows))
	top := 0
	kids := map[int]int{} // children seen so far per parent index
	for _, jr := range rows {
		m := jr.cols
		cols := map[string]string{}
		for k, v := range m {
			if to, ok := rename[k]; ok {
				k = to
			}
			if slices.Contains(exportOnlyColumns(), k) {
				if v != "" && !slices.Contains(ignored, k) {
					ignored = append(ignored, k)
				}
				continue
			}
			if !slices.Contains(importColumns(), k) {
				return nil, nil, errs.New(errs.InvalidInput, "task import: unknown column "+strconv.Quote(k)+
					"; map it with --map "+k+"=<field> (fields: "+strings.Join(importColumns(), ", ")+")")
			}
			cols[k] = v
		}
		ext := strings.TrimSpace(cols[colExtID])
		if jr.parent < 0 {
			top++
		} else {
			kids[jr.parent]++
		}
		if ext == "" && jr.parent < 0 {
			ext = base + extIDRowSep + strconv.Itoa(top)
		} else if ext == "" {
			ext = out[jr.parent].extID + extIDKidSep + strconv.Itoa(kids[jr.parent])
		}
		out = append(out, importRecord{extID: ext, cols: cols, parent: jr.parent, checklist: jr.checklist, line: jr.line})
	}
	out, err := linkParentColumn(out)
	if err != nil {
		return nil, nil, err
	}
	return out, ignored, nil
}

// linkParentColumn links a parent column (task export --format csv) that
// names another record of the file, before or after it, like a nested
// child (G-TASK-3); any other value stays a task id the import checks in
// the target. The records are then ordered parents first (file order
// otherwise kept), so a parent exists when its child is added; a cycle
// in the file is refused.
func linkParentColumn(recs []importRecord) ([]importRecord, error) {
	byExt := map[string]int{}
	for i, r := range recs {
		if _, dup := byExt[r.extID]; !dup {
			byExt[r.extID] = i
		}
	}
	linked := false
	for i := range recs {
		p := strings.TrimSpace(recs[i].cols[colParent])
		if p == "" || recs[i].parent >= 0 {
			continue
		}
		if j, ok := byExt[p]; ok && j != i {
			recs[i].parent = j
			delete(recs[i].cols, colParent)
			linked = true
		}
	}
	if !linked {
		return recs, nil
	}
	const (
		unseen = iota
		visiting
		placed
	)
	state := make([]int, len(recs))
	newIdx := make([]int, len(recs))
	order := make([]int, 0, len(recs))
	var place func(i int) error
	place = func(i int) error {
		switch state[i] {
		case placed:
			return nil
		case visiting:
			return errs.New(errs.InvalidInput, "task import: the parent column makes a cycle at "+recs[i].extID).
				WithHint("give each task a parent that is not its own sub-task")
		}
		state[i] = visiting
		if p := recs[i].parent; p >= 0 {
			if err := place(p); err != nil {
				return err
			}
		}
		state[i] = placed
		newIdx[i] = len(order)
		order = append(order, i)
		return nil
	}
	for i := range recs {
		if err := place(i); err != nil {
			return nil, err
		}
	}
	out := make([]importRecord, len(recs))
	for at, i := range order {
		r := recs[i]
		if r.parent >= 0 {
			r.parent = newIdx[r.parent]
		}
		out[at] = r
	}
	return out, nil
}

// jsonRow is one task object of a JSON import, flattened in pre-order.
type jsonRow struct {
	cols      map[string]string
	parent    int
	checklist []importItem
	// line is the CSV file line of the row (0 for JSON).
	line int
}

// csvRows reads a CSV with a header row, and each row's first file line
// (header = 1). A row whose cells do not fit the header is listed with the
// others; a broken quote stops the read, naming its line and the reason.
func csvRows(path string, raw []byte) ([]map[string]string, []int, error) {
	cr := csv.NewReader(bytes.NewReader(raw))
	recheck := "lore task import --file " + path + " --dry-run"
	head, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fromfile.CSV("task import --file", path, raw, err, recheck)
	}
	for i, h := range head {
		head[i] = strings.TrimSpace(strings.TrimPrefix(h, utf8BOM))
	}
	var out []map[string]string
	var lines []int
	var bad []fromfile.Problem
	for {
		r, rerr := cr.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			p, ok := fromfile.CSVProblem(raw, rerr)
			if !ok || !errors.Is(rerr, csv.ErrFieldCount) {
				if !ok {
					p = fromfile.Problem{At: "the file", Reason: rerr.Error()}
				}
				return nil, nil, fromfile.Refuse("task import --file", path, append(bad, p), recheck)
			}
			p.Reason = fmt.Sprintf("it has %d cells, the header has %d", len(r), len(head))
			bad = append(bad, p)
			continue
		}
		m := map[string]string{}
		for i, v := range r {
			if v != "" {
				m[head[i]] = v
			}
		}
		line, _ := cr.FieldPos(0)
		out = append(out, m)
		lines = append(lines, line)
	}
	if e := fromfile.Refuse("task import --file", path, bad, recheck); e != nil {
		return nil, nil, e
	}
	return out, lines, nil
}

// jsonRows flattens a JSON array of task objects in pre-order. Values are
// strings or numbers (a number is kept as written, e.g. an id 412); the
// keys "children" (nested task objects) and "checklist" ([{text, done}])
// read back task export --with-children / --with-checklist. out is the
// rows so far, so a child's parent is its index in the final list.
func jsonRows(objs []map[string]json.RawMessage, parent int, out []jsonRow) ([]jsonRow, error) {
	for i, o := range objs {
		jr := jsonRow{cols: map[string]string{}, parent: parent}
		var kids []map[string]json.RawMessage
		for k, v := range o {
			switch k {
			case keyChildren:
				if err := json.Unmarshal(v, &kids); err != nil {
					return nil, errs.Wrap(errs.InvalidInput, "task import: item "+strconv.Itoa(i+1)+" children must be an array of task objects", err)
				}
				continue
			case keyChecklist:
				if err := json.Unmarshal(v, &jr.checklist); err != nil {
					return nil, errs.Wrap(errs.InvalidInput, "task import: item "+strconv.Itoa(i+1)+` checklist must be [{"text": "…", "done": false}]`, err)
				}
				continue
			}
			s, ok, err := jsonScalar(v)
			if err != nil {
				return nil, errs.New(errs.InvalidInput, "task import: item "+strconv.Itoa(i+1)+" field "+strconv.Quote(k)+" must be a string or a number")
			}
			if ok {
				jr.cols[k] = s
			}
		}
		out = append(out, jr)
		if len(kids) > 0 {
			var err error
			if out, err = jsonRows(kids, len(out)-1, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// jsonScalar reads a string or number value; ok is false for null or "".
func jsonScalar(v json.RawMessage) (string, bool, error) {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s, s != "", nil
	}
	var n json.Number
	if err := json.Unmarshal(v, &n); err == nil {
		return n.String(), true, nil
	}
	if string(bytes.TrimSpace(v)) == "null" {
		return "", false, nil
	}
	return "", false, errs.New(errs.InvalidInput, "not a string or a number")
}

// checkRecord is the part of task add's checks that needs no store, run on
// every record before the first write.
func checkRecord(r importRecord) error {
	if strings.TrimSpace(r.cols[colTitle]) == "" {
		return errs.New(errs.InvalidInput, "task import: "+r.extID+" has no title").WithHint("give it a title")
	}
	if p := r.cols[colPriority]; p != "" {
		if err := work.OneOf(Noun, colPriority, p, schema.TaskPriorities()); err != nil {
			return errors.Join(err, errs.New(errs.InvalidInput, "task import: in "+r.extID))
		}
	}
	if st := r.cols[colStatus]; st != "" {
		if err := work.OneOf(Noun, colStatus, st, schema.TaskStatuses()); err != nil {
			return errors.Join(err, errs.New(errs.InvalidInput, "task import: in "+r.extID))
		}
	}
	if c := r.cols[colCommitment]; c != "" {
		if err := work.OneOf(Noun, colCommitment, c, schema.TaskCommitments()); err != nil {
			return errors.Join(err, errs.New(errs.InvalidInput, "task import: in "+r.extID))
		}
	}
	if len(r.checklist) > checklist.MaxItems {
		return errs.New(errs.InvalidInput, "task import: "+r.extID+" has "+strconv.Itoa(len(r.checklist))+" checklist items, over "+strconv.Itoa(checklist.MaxItems))
	}
	for _, it := range r.checklist {
		if _, err := checklist.CleanText(it.Text); err != nil {
			return errors.Join(err, errs.New(errs.InvalidInput, "task import: in "+r.extID))
		}
	}
	for _, d := range []string{colDue, colDeferUntil} {
		if v := r.cols[d]; v != "" {
			// A day, or an RFC 3339 datetime (what export writes for a due
			// with a clock time, OV53); --due reads both.
			if _, err := work.Date(d, v); err != nil {
				if _, terr := time.Parse(time.RFC3339, v); terr != nil {
					return errors.Join(err, errs.New(errs.InvalidInput, "task import: in "+r.extID))
				}
			}
		}
	}
	return nil
}

// DuplicateImport is one external id that more than one live task was
// imported under (PROV9): two clones importing the same source before
// either synced, or the same file imported twice.
type DuplicateImport struct {
	Source string
	ExtID  string
	// IDs are the tasks, oldest first (ids are UUIDv7: time order).
	IDs []string
}

// FixCommand is what `lore doctor` prints for d until a merge command
// exists (PROV9, Q-R3-1): delete the newer copies, keep the oldest.
func (d DuplicateImport) FixCommand() []string {
	out := make([]string, 0, len(d.IDs)-1)
	for _, id := range d.IDs[1:] {
		out = append(out, "lore task delete --id "+id+" --yes")
	}
	return out
}

// FindDuplicateImports lists every (import source or src, ext_id) that
// more than one live task in the attached stores carries. PROV9: they are
// reported, never merged on their own, because the copies may have been
// edited differently.
func FindDuplicateImports(ctx context.Context, env work.Env) ([]DuplicateImport, error) {
	rows, err := work.ListAll(ctx, env, table, "")
	if err != nil {
		return nil, err
	}
	groups := map[origin.ImportKey][]string{}
	var order []origin.ImportKey
	for _, s := range rows {
		k, ok := importKey(s.Row)
		if !ok {
			continue
		}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], s.Row.ID.String())
	}
	var out []DuplicateImport
	for _, k := range order {
		if got := groups[k]; len(got) > 1 {
			slices.Sort(got)
			out = append(out, DuplicateImport{Source: k.Source, ExtID: k.ExtID, IDs: got})
		}
	}
	return out, nil
}

// importKey reads r's import key (PROVENANCE 11.4, through the one
// reader origin.ImportOf); ok is false for a row not imported.
func importKey(r row.Row) (origin.ImportKey, bool) {
	imp, ok := origin.ImportOf(r.Origin)
	if !ok {
		return origin.ImportKey{}, false
	}
	return imp.Key(), true
}
