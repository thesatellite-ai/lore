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

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/errs"
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
}

// ImportOut is the output of task.import.
type ImportOut struct {
	Batch  string      `json:"batch,omitempty"`
	Source string      `json:"source"`
	DryRun bool        `json:"dry_run,omitempty"`
	Count  int         `json:"count"`
	Rows   []ImportRow `json:"rows"`
}

// Text implements op.Texter.
func (o ImportOut) Text() string {
	if o.DryRun {
		var b strings.Builder
		b.WriteString("would import " + strconv.Itoa(o.Count) + " tasks from " + o.Source + " (dry run, nothing written)\n")
		for _, r := range o.Rows {
			b.WriteString("  " + r.ExtID + "  " + r.Title + "\n")
		}
		return b.String()
	}
	return work.Success + " " + strconv.Itoa(o.Count) + " tasks imported from " + o.Source + " (batch " + o.Batch + ")\n"
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

// importRecord is one parsed task of the file.
type importRecord struct {
	extID string
	cols  map[string]string
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
		out.Rows = append(out.Rows, ImportRow{ExtID: r.extID, Title: strings.TrimSpace(r.cols[colTitle])})
		items = append(items, bulk.Item{ID: r.extID, Label: strings.TrimSpace(r.cols[colTitle])})
	}
	out.Count = len(recs)
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
		got, aerr := o.add(rctx, add)
		if aerr != nil {
			return ImportOut{}, errors.Join(aerr, errs.New(errs.InvalidInput, "task import: stopped at "+r.extID+" after "+
				strconv.Itoa(i)+" of "+strconv.Itoa(len(recs))+" tasks; the ones written are in batch "+out.Batch))
		}
		out.Rows[i].ID = got.Data.ID
	}
	return out, nil
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
	var rows []map[string]string
	var err error
	if from == FromCSV {
		rows, err = csvRows(raw)
	} else {
		rows, err = jsonRows(raw)
	}
	if err != nil {
		return nil, err
	}
	out := make([]importRecord, 0, len(rows))
	for i, m := range rows {
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
		if ext == "" {
			ext = base + extIDRowSep + strconv.Itoa(i+1)
		}
		out = append(out, importRecord{extID: ext, cols: cols})
	}
	return out, nil
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

// jsonRows reads a JSON array of objects whose values are strings or
// numbers (a number is kept as written, e.g. an id 412).
func jsonRows(raw []byte) ([]map[string]string, error) {
	var objs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &objs); err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "task import: the JSON must be an array of objects", err)
	}
	out := make([]map[string]string, 0, len(objs))
	for i, o := range objs {
		m := map[string]string{}
		for k, v := range o {
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				if s != "" {
					m[k] = s
				}
				continue
			}
			var n json.Number
			if err := json.Unmarshal(v, &n); err == nil {
				m[k] = n.String()
				continue
			}
			if string(bytes.TrimSpace(v)) == "null" {
				continue
			}
			return nil, errs.New(errs.InvalidInput, "task import: item "+strconv.Itoa(i+1)+" field "+strconv.Quote(k)+" must be a string or a number")
		}
		out = append(out, m)
	}
	return out, nil
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
	for _, d := range []string{colDue, colDeferUntil} {
		if v := r.cols[d]; v != "" {
			if _, err := work.Date(d, v); err != nil {
				return errors.Join(err, errs.New(errs.InvalidInput, "task import: in "+r.extID))
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

// importSlot is the `_o.import` object as this file reads it (the keys of
// logic/origin's import block).
type importSlot struct {
	Import *struct {
		Source string `json:"source"`
		Src    string `json:"src"`
		ExtID  string `json:"ext_id"`
	} `json:"import"`
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
	groups := map[importID][]string{}
	var order []importID
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
			out = append(out, DuplicateImport{Source: k.source, ExtID: k.ext, IDs: got})
		}
	}
	return out, nil
}

// importID is PROVENANCE 11.4's import key: (import.src, ext_id), or
// (import.source, ext_id) when no source record exists.
type importID struct{ source, ext string }

// importKey reads r's import key; ok is false for a row not imported.
func importKey(r row.Row) (importID, bool) {
	var zero importID
	if r.Origin.IsZero() {
		return zero, false
	}
	var slot importSlot
	if err := json.Unmarshal(r.Origin.Raw(), &slot); err != nil || slot.Import == nil || slot.Import.ExtID == "" {
		return zero, false
	}
	src := slot.Import.Src
	if src == "" {
		src = slot.Import.Source
	}
	return importID{src, slot.Import.ExtID}, true
}
