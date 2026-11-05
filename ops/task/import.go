package task

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/checklist"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
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
)

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
	return []string{colExtID, colTitle, colBody, colPriority, colCommitment, colDue, colDeferUntil, colTasklist, colMission, colPlan, colRole, colKey}
}

// ImportIn is the input of task.import.
type ImportIn struct {
	File   string   `json:"file" arg:"0" desc:"the file to import"`
	From   string   `json:"from,omitempty" flag:"from" desc:"csv | json (default: from the file extension)"`
	Map    []string `json:"map,omitempty" flag:"map" desc:"rename columns: <file column>=<task field>[,…] (fields: id title body priority commitment due defer_until tasklist mission plan role key)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would be created; write nothing"`
	To     string   `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
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
		b.WriteString("would import " + strconv.Itoa(o.Count) + " tasks from " + o.Source + " (dry run, nothing written)\n")
		for _, r := range o.Rows {
			b.WriteString("  " + r.ExtID + "  " + r.Title + "\n")
		}
		o.warnLines(&b)
		return b.String()
	}
	var b strings.Builder
	b.WriteString(work.Success + " " + strconv.Itoa(o.Count) + " tasks imported from " + o.Source + " (batch " + o.Batch + ")\n")
	o.warnLines(&b)
	return b.String()
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
			Examples: []op.Example{{Title: "import a CSV", CLI: "lore task import --from csv tasks.csv --yes", Want: "imported"}},
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
	recs, err := parseImport(from, filepath.Base(in.File), raw, rename)
	if err != nil {
		return ImportOut{}, err
	}
	out := ImportOut{Source: from, DryRun: in.DryRun, Rows: make([]ImportRow, 0, len(recs))}
	items := make([]bulk.Item, 0, len(recs))
	for _, r := range recs {
		if err = checkRecord(r); err != nil {
			return ImportOut{}, err
		}
		ir := ImportRow{ExtID: r.extID, Title: strings.TrimSpace(r.cols[colTitle]), Checklist: len(r.checklist)}
		if r.parent >= 0 {
			ir.ParentExtID = recs[r.parent].extID
		}
		out.Rows = append(out.Rows, ir)
		items = append(items, bulk.Item{ID: r.extID, Label: strings.TrimSpace(r.cols[colTitle])})
	}
	out.Count = len(recs)
	// PC-TASK-1: a tasklist / mission / plan id the target does not hold
	// (an export from another repo) is dropped with a warning and a note.
	dropped, err := o.dropDanglingRefs(ctx, recs)
	if err != nil {
		return ImportOut{}, err
	}
	for i, ds := range dropped {
		for _, d := range ds {
			out.Warnings = append(out.Warnings, Warning{Code: WarnImportDroppedRef, Field: d.field,
				Message: recs[i].extID + ": dropped " + d.field + " " + d.id + " (not in this repo)"})
		}
	}
	if in.DryRun || len(recs) == 0 {
		return out, nil
	}
	target := store.Source(in.To)
	if target == "" {
		target = store.SourceRepo
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
	// The default list is created (if needed) outside the import slot: it
	// is not an imported row and must not carry _o.import.
	defList, err := o.defaultTasklist(bctx, in.To, project)
	if err != nil {
		return ImportOut{}, err
	}
	for i, r := range recs {
		// XQ9 / PROVENANCE 5.1: the row's _o.import names the importer
		// kind and the external id (no ext_ source record for a file).
		rctx := engine.WithImport(bctx, origin.Import{Source: from, ExtID: r.extID})
		add := AddIn{Title: r.cols[colTitle], Body: r.cols[colBody], Priority: r.cols[colPriority], Commitment: r.cols[colCommitment],
			Due: r.cols[colDue], DeferUntil: r.cols[colDeferUntil], Tasklist: orDefault(r.cols[colTasklist], defList),
			Mission: r.cols[colMission], Plan: r.cols[colPlan], Role: r.cols[colRole], Key: r.cols[colKey], To: in.To}
		if r.parent >= 0 {
			// TASK 6.5: a child without its own tasklist inherits the
			// parent's instead of the import default.
			add.Parent, add.Tasklist = out.Rows[r.parent].ID, r.cols[colTasklist]
		}
		got, aerr := o.add(rctx, add)
		if aerr == nil && len(r.checklist) > 0 {
			aerr = o.importChecklist(rctx, got.Data.ID, r.checklist)
		}
		if aerr != nil {
			return ImportOut{}, errors.Join(aerr, errs.New(errs.InvalidInput, "task import: stopped at "+r.extID+" after "+
				strconv.Itoa(i)+" of "+strconv.Itoa(len(recs))+" tasks; the ones written are in batch "+out.Batch))
		}
		out.Rows[i].ID = got.Data.ID
		for _, d := range dropped[i] {
			// PC-TASK-1: the row says what it lost, outside the import
			// slot (the note is not an imported row).
			if err = o.addNote(bctx, got.Data.ID, d.note()); err != nil {
				return ImportOut{}, err
			}
		}
	}
	return out, nil
}

// droppedRef is one reference an import dropped (PC-TASK-1).
type droppedRef struct {
	field string // the task field: tasklist_id, mission_id, plan_id
	id    string
}

// note is the comment the imported task gets for d.
func (d droppedRef) note() string {
	return "import: dropped " + d.field + " " + d.id + " (not in this repo)"
}

// dropDanglingRefs clears every tasklist / mission / plan column whose id
// is well formed but names no row in the attached stores, and returns
// what it cleared per record. A malformed id is left for task add to
// refuse; an id that exists (even in the trash) is kept, so add's own
// checks apply. The import file creates no tasklists, missions or plans,
// so the target is the only place such an id can exist (PC-TASK-1).
func (o ops) dropDanglingRefs(ctx context.Context, recs []importRecord) (map[int][]droppedRef, error) {
	refCols := []struct {
		col, field string
		tbl        row.Table
	}{
		{colTasklist, work.FieldTasklist, schema.TableTasklists},
		{colMission, work.FieldMission, schema.TableMissions},
		{colPlan, work.FieldPlan, schema.TablePlans},
	}
	out := map[int][]droppedRef{}
	for i, r := range recs {
		for _, c := range refCols {
			raw := strings.TrimSpace(r.cols[c.col])
			if raw == "" {
				continue
			}
			id, perr := ids.ParseAny(raw)
			if perr != nil {
				continue
			}
			_, _, gerr := o.env.Get(ctx, engine.Ref{Table: c.tbl, ID: id})
			if gerr == nil {
				continue
			}
			if !errs.Has(gerr, errs.NotFound) {
				return nil, gerr
			}
			delete(r.cols, c.col)
			out[i] = append(out[i], droppedRef{field: c.field, id: raw})
		}
	}
	return out, nil
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
		return nil, errs.Wrap(errs.InvalidInput, "task import: open "+path, err)
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
func parseImport(from, base string, raw []byte, rename map[string]string) ([]importRecord, error) {
	var rows []jsonRow
	if from == FromCSV {
		flat, err := csvRows(raw)
		if err != nil {
			return nil, err
		}
		for _, m := range flat {
			rows = append(rows, jsonRow{cols: m, parent: -1})
		}
	} else {
		var objs []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &objs); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "task import: the JSON must be an array of objects", err)
		}
		var err error
		if rows, err = jsonRows(objs, -1, nil); err != nil {
			return nil, err
		}
	}
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
			if !slices.Contains(importColumns(), k) {
				return nil, errs.New(errs.InvalidInput, "task import: unknown column "+strconv.Quote(k)+
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
		out = append(out, importRecord{extID: ext, cols: cols, parent: jr.parent, checklist: jr.checklist})
	}
	return out, nil
}

// jsonRow is one task object of a JSON import, flattened in pre-order.
type jsonRow struct {
	cols      map[string]string
	parent    int
	checklist []importItem
}

// csvRows reads a CSV with a header row.
func csvRows(raw []byte) ([]map[string]string, error) {
	recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "task import: the CSV does not parse", err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	head := make([]string, len(recs[0]))
	for i, h := range recs[0] {
		head[i] = strings.TrimSpace(strings.TrimPrefix(h, utf8BOM))
	}
	out := make([]map[string]string, 0, len(recs)-1)
	for _, r := range recs[1:] {
		m := map[string]string{}
		for i, v := range r {
			if v != "" {
				m[head[i]] = v
			}
		}
		out = append(out, m)
	}
	return out, nil
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
		return errs.New(errs.InvalidInput, "task import: "+r.extID+" has no title")
	}
	if p := r.cols[colPriority]; p != "" {
		if err := work.OneOf(Noun, colPriority, p, schema.TaskPriorities()); err != nil {
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
		out = append(out, "lore task delete "+id+" --yes")
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
