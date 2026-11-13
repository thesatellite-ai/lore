package settings

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/fromfile"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/resname"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Resource import, export, repoint and clone (RESOURCES_DESIGN 3.20, TSQ8):
//
//	import <file>  read (csv|json|yaml) → records → check every record (kind,
//	               name, values; refs by name in the file or upward) →
//	               ask (always) → one bat_ of kind import → per record, in
//	               ref order: resAdd (or resEdit with --upsert-by name),
//	               each with _o.import {source, ext_id} (PROVENANCE 5.1)
//	export         list rows of --kind (--where) → the import format
//	               (sensitive masked unless --show-sensitive; ••• is refused
//	               on re-import)
//	repoint a b    every ref field naming a → b, one bat_, then archive a
//	clone a b      a's values → resAdd b (--set overrides)
const (
	OpResImport  = "resource.import"
	OpResExport  = "resource.export"
	OpResRepoint = "resource.repoint"
	OpResClone   = "resource.clone"
)

// Import/export formats (TSQ8: CSV, JSON and YAML stay in core; other
// shapes come from importer plugins).
const (
	FormatCSV  = "csv"
	FormatJSON = "json"
	FormatYAML = "yaml"
	formatYML  = "yml"
	// upsertByName is the one --upsert-by key (3.20).
	upsertByName = "name"
	// maxResImportBytes caps an import file (the same 4 MB as task import).
	maxResImportBytes = 4 << 20
	// csvName is the CSV column holding the resource name.
	csvName = "name"
)

// ResImportIn is the input of resource.import.
type ResImportIn struct {
	File       string `json:"file" arg:"0" desc:"the file to import (csv, json or yaml)"`
	Kind       string `json:"kind,omitempty" flag:"kind" desc:"the kind of every row (required for csv; json/yaml rows name their kind)"`
	From       string `json:"from,omitempty" flag:"from" desc:"csv, json or yaml (default: from the file extension)"`
	UpsertBy   string `json:"upsert_by,omitempty" flag:"upsert-by" desc:"name: update rows with the same (place, kind, name), create the rest"`
	To         string `json:"to,omitempty" flag:"to" desc:"where the rows are written (repo, <store>, <store>/<folder>; default repo)"`
	DryRun     bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"check and list what would be written; write nothing"`
	SkipErrors bool   `json:"skip_errors,omitempty" flag:"skip-errors" desc:"write the valid rows and list the rest (default: all or nothing)"`
	Overwrite  bool   `json:"overwrite,omitempty" flag:"overwrite" desc:"re-import: also replace rows edited in lore since the last import (default: keep them)"`
}

// ImportLine is one record of an import as reported.
type ImportLine struct {
	Action string `json:"action"` // create | update | kept | error | skipped
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	ID     string `json:"id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Import line actions.
const (
	actCreate  = "create"
	actUpdate  = "update"
	actError   = "error"
	actSkipped = "skipped"
	// actKept: a re-imported row edited in lore since its last import is
	// left alone (PROVENANCE 11.4; --overwrite replaces it).
	actKept = "kept"
)

// ResImportOut is the output of resource.import.
type ResImportOut struct {
	Source  string       `json:"source"`
	DryRun  bool         `json:"dry_run,omitempty"`
	Batch   string       `json:"batch,omitempty"`
	Created int          `json:"created"`
	Updated int          `json:"updated"`
	Kept    int          `json:"kept,omitempty"`
	Errors  int          `json:"errors"`
	Rows    []ImportLine `json:"rows"`
}

// Text implements op.Texter.
func (o ResImportOut) Text() string {
	var b strings.Builder
	for _, r := range o.Rows {
		line := "  " + r.Action + "  " + r.Kind + " " + r.Name
		if r.Error != "" {
			line += "  " + r.Error
		}
		b.WriteString(line + "\n")
	}
	switch {
	case o.DryRun:
		b.WriteString("nothing written (dry run)\n")
	case o.Batch == "":
		b.WriteString("nothing written\n")
	default:
		fmt.Fprintf(&b, "imported %s (%d created, %d updated) from %s; batch %s\n", plural.Count(o.Created+o.Updated, "row"), o.Created, o.Updated, o.Source, o.Batch)
	}
	if o.Kept > 0 {
		fmt.Fprintf(&b, "kept %s edited in lore since the last import (use --overwrite to replace them)\n", plural.Count(o.Kept, "row"))
	}
	if o.Errors > 0 {
		fmt.Fprintf(&b, "%s (skipped)\n", plural.Count(o.Errors, "error"))
	}
	return b.String()
}

// importDoc is the json/yaml import shape (3.20): {"resources": [...]}.
type importDoc struct {
	Resources []importElem `json:"resources" yaml:"resources"`
}

// importElem is one element of importDoc. Fields are decoded per field:
// a string is the CLI text of --set, anything else its JSON (--set-json).
type importElem struct {
	Kind   string                     `json:"kind" yaml:"kind"`
	Name   string                     `json:"name" yaml:"name"`
	Fields map[string]json.RawMessage `json:"fields" yaml:"-"`
}

// resRecord is one parsed record: a kind, a name and field=value pairs in
// --set form, plus its external id for _o.import.
type resRecord struct {
	kind, name, extID string
	sets              []string
}

func (o ops) resourceIOOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpResImport, Summary: "Import resources from a csv, json or yaml file (one batch, all or nothing)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.ResourceName, errs.KindUnknown, errs.KindConflict, errs.ResourceStale, errs.ResourcePlace, errs.ResourceTaken, errs.ResourceBatch), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "import hosts", CLI: "lore resource import --file hosts.csv --kind host --yes", Want: "imported"}}}, o.resImport),
		op.Define(op.Spec{Name: OpResExport, Summary: "Export resources of a kind in the import format (sensitive values masked)", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindUnknown, errs.NeedsConfirm), Confirm: op.ConfirmRule{When: "show_sensitive"}, Examples: []op.Example{{Title: "every host as CSV", CLI: "lore resource export --kind host --format csv"}}}, o.resExport),
		op.Define(op.Spec{Name: OpResRepoint, Summary: "Move every ref from one resource to another of the same kind, then archive the old one", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.ResourceKindMismatch), Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "move references to a new resource (preview)", CLI: "lore resource repoint --old prod_db --new primary_db --dry-run"}}}, o.resRepoint),
		op.Define(op.Spec{Name: OpResClone, Summary: "Add a resource with another resource's values (--set overrides)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.ResourceName, errs.ResourcePlace, errs.ResourceTaken), Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "a staging copy of a resource", CLI: "lore resource clone --from prod_db --name staging_db --set host=staging.example.com"}}}, o.resClone),
	}
}

// formatOf settles --from / --format: given, else from the file extension.
func formatOf(given, file string) (string, error) {
	f := strings.ToLower(given)
	if f == "" {
		f = strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
	}
	if f == formatYML {
		f = FormatYAML
	}
	switch f {
	case FormatCSV, FormatJSON, FormatYAML:
		return f, nil
	default:
		return "", errs.New(errs.InvalidInput, "resource import: unknown format "+quote(f)).WithHint("--from csv, json or yaml (other shapes come from importer plugins)")
	}
}

func readResFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errs.New(errs.InvalidInput, "resource import: name the file to import")
	}
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "resource import: open "+path, err)
	}
	defer func() { _ = fh.Close() }() // read only
	b, err := io.ReadAll(io.LimitReader(fh, maxResImportBytes+1))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "resource import: read "+path, err)
	}
	if len(b) > maxResImportBytes {
		return nil, errs.New(errs.InvalidInput, "resource import: "+path+" is over "+strconv.Itoa(maxResImportBytes)+" bytes")
	}
	return b, nil
}

// parseResImport turns the file into records. ext ids are "<kind>/<name>"
// so a re-import names the same external ids (PROVENANCE 11.4).
func parseResImport(format, kind, path string, raw []byte) ([]resRecord, error) {
	var out []resRecord
	switch format {
	case FormatCSV:
		if kind == "" {
			return nil, errs.New(errs.InvalidInput, "resource import: a csv file holds one kind; give --kind")
		}
		rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
		if err != nil {
			return nil, fromfile.CSV("resource import --file", path, raw, err, "lore resource import --file "+path+" --kind "+kind+" --dry-run")
		}
		if len(rows) == 0 {
			return nil, nil
		}
		head := rows[0]
		ni := slices.Index(head, csvName)
		if ni < 0 {
			return nil, errs.New(errs.InvalidInput, "resource import: the csv header has no name column")
		}
		for _, r := range rows[1:] {
			rec := resRecord{kind: kind}
			for i, cell := range r {
				switch {
				case i >= len(head):
				case i == ni:
					rec.name = strings.TrimSpace(cell)
				case strings.TrimSpace(cell) != "":
					rec.sets = append(rec.sets, head[i]+setSep+cell)
				}
			}
			out = append(out, rec)
		}
	case FormatJSON, FormatYAML:
		var doc importDoc
		if err := decodeDoc(format, raw, &doc); err != nil {
			return nil, err
		}
		for _, e := range doc.Resources {
			k := e.Kind
			if k == "" {
				k = kind
			}
			rec := resRecord{kind: k, name: e.Name}
			for _, f := range slices.Sorted(maps.Keys(e.Fields)) {
				v := e.Fields[f]
				var s string
				if json.Unmarshal(v, &s) == nil {
					rec.sets = append(rec.sets, f+setSep+s)
				} else {
					rec.sets = append(rec.sets, f+setSep+string(v))
				}
			}
			out = append(out, rec)
		}
	}
	for i := range out {
		out[i].extID = out[i].kind + "/" + out[i].name
	}
	return out, nil
}

// decodeDoc reads json, or yaml re-encoded through JSON so both formats
// share one field decoder.
func decodeDoc(format string, raw []byte, doc *importDoc) error {
	if format == FormatJSON {
		if err := json.Unmarshal(raw, doc); err != nil {
			return errs.Wrap(errs.InvalidInput, "resource import: json", err)
		}
		return nil
	}
	var y struct {
		Resources []struct {
			Kind   string               `yaml:"kind"`
			Name   string               `yaml:"name"`
			Fields map[string]yaml.Node `yaml:"fields"`
		} `yaml:"resources"`
	}
	if err := yamlerr.Decode(raw, &y, yamlerr.Lenient); err != nil { // line + key, never a Go type
		return errs.Wrap(errs.InvalidInput, "resource import: the YAML is not valid: "+err.Error(), err)
	}
	for _, e := range y.Resources {
		el := importElem{Kind: e.Kind, Name: e.Name, Fields: map[string]json.RawMessage{}}
		for k, v := range e.Fields {
			b, err := nodeJSON(&v)
			if err != nil {
				return errs.Wrap(errs.InvalidInput, "resource import: yaml field "+k, err)
			}
			el.Fields[k] = b
		}
		doc.Resources = append(doc.Resources, el)
	}
	return nil
}

// inFileQualified rewrites, in place, every set value that names a record
// of the same file by its qualified or short name (RES 3.4:
// <place>:<kind>/<name> with the import's target place, or <kind>/<name>)
// to that record's bare name, so the two-pass plan (checkRecord, refOrder,
// importPlan) sees it as an in-file ref. The bare name is then resolved by
// pendingRow first, which only matches the file's own rows, so it cannot
// fall through to a same-name row at another level. A value that is
// already a record's bare name is left as it is.
func inFileQualified(recs []resRecord, target store.Source) {
	byKey := map[resname.Key]string{}
	names := map[string]bool{}
	for _, r := range recs {
		names[r.name] = true
		byKey[resname.Key{Kind: r.kind, Name: r.name}] = r.name
	}
	for i := range recs {
		for j, set := range recs[i].sets {
			f, v, ok := strings.Cut(set, setSep)
			if !ok || names[v] {
				continue
			}
			for _, k := range resname.Forms(v) {
				n, hit := byKey[resname.Key{Kind: k.Kind, Name: k.Name}]
				if hit && (k.Place == "" || resname.PlaceMatches(target, k.Place)) {
					recs[i].sets[j] = f + setSep + n
					break
				}
			}
		}
	}
}

// checkRecord validates a record without writing: the kind, the name, no
// masked value, and every value by its field's type. A ref whose target is
// another record of the file is accepted here (it is written first).
func (o ops) checkRecord(ctx context.Context, r resRecord, inFile map[string]bool) error {
	d, err := o.kindDef(ctx, r.kind)
	if err != nil {
		return err
	}
	if err = checkResourceName(d, r.name); err != nil {
		return err
	}
	vals := map[string]json.RawMessage{}
	for _, s := range r.sets {
		f, raw, _ := strings.Cut(s, setSep)
		if raw == maskedValue {
			return invalid(d, r.name, f+" is masked (•••): an exported masked value cannot be imported")
		}
		def, ok := d.byName[f]
		if ok && text(def.Row, fType) == string(value.KindRef) && inFile[raw] {
			vals[def.Row.ID.String()] = json.RawMessage(`""`)
			continue
		}
		if err = o.applySets(ctx, d, r.name, []string{s}, vals); err != nil {
			return err
		}
	}
	return requireFields(d, r.name, vals)
}

// refOrder sorts records so a record comes after every record of the file
// its ref values name (refs by name, any order in the file, 3.20). Records
// in a ref cycle keep file order at the end; importPlan defers the refs
// that would point forward.
func refOrder(recs []resRecord) []resRecord {
	names := map[string]bool{}
	for _, r := range recs {
		names[r.name] = true
	}
	done := map[string]bool{}
	var out []resRecord
	pending := slices.Clone(recs)
	for len(pending) > 0 {
		var next []resRecord
		for _, r := range pending {
			ready := true
			for _, s := range r.sets {
				_, v, _ := strings.Cut(s, setSep)
				if names[v] && v != r.name && !done[v] {
					ready = false
				}
			}
			if ready {
				out, done[r.name] = append(out, r), true
			} else {
				next = append(next, r)
			}
		}
		if len(next) == len(pending) { // a cycle: keep file order
			return append(out, next...)
		}
		pending = next
	}
	return out
}

// plannedRecord is one record of an import as it is written (PC-RES-1):
// first holds every set written by pass 1, later the ref sets that name a
// record of the file not yet written when pass 1 reaches this one (the
// other end of a ref cycle, or the record itself), written by pass 2 once
// every record exists.
type plannedRecord struct {
	resRecord
	first, later []string
}

// refField reports whether field of kind is a ref field and whether it is
// required; ok is false when the kind or the field is unknown (the check
// pass has already refused those).
type refField func(kind, field string) (isRef, required, ok bool)

// importPlan orders recs (refOrder) and splits each record's sets into the
// two passes of PC-RES-1. A ref set is deferred only when its value is the
// name of a record of the file that pass 1 has not written yet; a ref to an
// existing row, or to a record written earlier, stays in pass 1.
//
// Refusal: RESOURCES defines no ref cycle as invalid data (3.4, 3.20, the
// graph cuts cycles, 4), so a cross-reference or a self reference always
// imports. The one cycle refused is one pass 1 cannot write at all: a
// REQUIRED ref field whose value is deferred, because the row cannot be
// created without it. That is E_RESOURCE_INVALID before the first write.
func importPlan(recs []resRecord, field refField) ([]plannedRecord, error) {
	inFile := map[string]bool{}
	for _, r := range recs {
		inFile[r.name] = true
	}
	written := map[string]bool{}
	out := make([]plannedRecord, 0, len(recs))
	for _, r := range refOrder(recs) {
		p := plannedRecord{resRecord: r}
		for _, s := range r.sets {
			f, v, _ := strings.Cut(s, setSep)
			isRef, required, ok := field(r.kind, f)
			if !ok || !isRef || !inFile[v] || written[v] {
				p.first = append(p.first, s)
				continue
			}
			if required {
				return nil, errs.New(errs.ResourceInvalid, r.kind+" "+r.name+": required ref "+f+" names "+quote(v)+
					", which needs "+r.name+" first (a cycle of required refs cannot be written)").
					WithHint("make " + f + " optional, or import " + v + " without its ref first")
			}
			p.later = append(p.later, s)
		}
		written[r.name] = true
		out = append(out, p)
	}
	return out, nil
}

// refFieldOf answers refField from the kinds the store defines.
func (o ops) refFieldOf(ctx context.Context) refField {
	defs := map[string]kindDef{}
	return func(kind, field string) (bool, bool, bool) {
		d, seen := defs[kind]
		if !seen {
			var err error
			if d, err = o.kindDef(ctx, kind); err != nil {
				return false, false, false
			}
			defs[kind] = d
		}
		f, ok := d.byName[field]
		if !ok {
			return false, false, false
		}
		return text(f.Row, fType) == string(value.KindRef), flag(f.Row, fRequired), true
	}
}

func (o ops) resImport(ctx context.Context, in ResImportIn) (ResImportOut, error) {
	format, err := formatOf(in.From, in.File)
	if err != nil {
		return ResImportOut{}, err
	}
	if in.UpsertBy != "" && in.UpsertBy != upsertByName {
		return ResImportOut{}, errs.New(errs.InvalidInput, "--upsert-by "+quote(in.UpsertBy)+": only name is supported")
	}
	raw, err := readResFile(in.File)
	if err != nil {
		return ResImportOut{}, err
	}
	recs, err := parseResImport(format, in.Kind, in.File, raw)
	if err != nil {
		return ResImportOut{}, err
	}
	target := targetOf(in.To)
	if err = checkPlace(ctx, o.env, target); err != nil {
		return ResImportOut{}, err
	}
	inFileQualified(recs, target)
	out := ResImportOut{Source: format, DryRun: in.DryRun, Rows: []ImportLine{}}
	inFile := map[string]bool{}
	for _, r := range recs {
		inFile[r.name] = true
	}
	// Check every record before the first write (all or nothing).
	existing := map[string]string{} // ext id → res id (upsert)
	var good []resRecord
	for _, r := range recs {
		line := ImportLine{Kind: r.kind, Name: r.name, Action: actCreate}
		cerr := o.checkRecord(ctx, r, inFile)
		if cerr == nil {
			if ex, found := o.existingIn(ctx, target, r); found {
				id := ex.Row.ID.String()
				// PROVENANCE 11.4: the same import key again is a re-import:
				// update in place when nobody edited the row since, else keep it.
				prev, imported := origin.ImportOf(ex.Row.Origin)
				reimport := imported && prev.Key() == (origin.Import{Source: format, ExtID: r.extID}).Key()
				switch {
				case reimport && !in.Overwrite && !importedLast(ex):
					line.Action, line.ID = actKept, id
					out.Kept++
					out.Rows = append(out.Rows, line)
					continue
				case reimport:
					line.Action, line.ID, existing[r.extID] = actUpdate, id, id
				case in.UpsertBy == "":
					cerr = errs.New(errs.ResourceTaken, r.kind+" "+r.name+" already exists in "+string(target)+" ("+id+")").WithHint("--upsert-by name updates it")
				default:
					line.Action, line.ID, existing[r.extID] = actUpdate, id, id
				}
			}
		}
		if cerr != nil {
			line.Action, line.Error = actError, cerr.Error()
			out.Errors++
			if !in.SkipErrors && !in.DryRun {
				return ResImportOut{}, batchRefusal(r.kind+" "+r.name, "nothing written (all or nothing; --skip-errors writes the valid rows)", cerr)
			}
		} else {
			good = append(good, r)
		}
		out.Rows = append(out.Rows, line)
	}
	// PC-RES-1: plan both passes before anything is written, so a cycle
	// that cannot be written is refused with nothing written (dry run too).
	plan, err := importPlan(good, o.refFieldOf(ctx))
	if err != nil {
		return ResImportOut{}, batchRefusal("", "nothing written (all or nothing)", err)
	}
	if in.DryRun {
		return out, nil
	}
	if len(good) == 0 {
		return out, nil // nothing to write (all kept or skipped)
	}
	// 3.16: an import always asks, naming the rows and the place.
	if err = bulk.Ask(ctx, "import "+plural.Count(len(good), "row")+" into "+string(target)+" as one batch?"); err != nil {
		return ResImportOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindImport)
	if err != nil {
		return ResImportOut{}, err
	}
	out.Batch = bid.String()
	// FB-RES-1 (PC-RES-1 "both or neither"): every new row gets its res_ id
	// before anything is written, so a ref to another row of the file
	// (forward, a cycle, itself) resolves to that id at once and each row
	// is written exactly once with all its refs. The writes are staged
	// (engine.Staging) and land in ONE backend batch at engine.Commit: a
	// row that fails while being written (a secret found, a policy, a
	// value) leaves the store exactly as it was.
	pend, err := o.pendingOf(ctx, target, plan, existing)
	if err != nil {
		return ResImportOut{}, err
	}
	sctx := withPending(engine.Staging(bctx), pend)
	// a refused import (any return before Commit) closes its stage, so the
	// open-stage tracking does not keep its rows for the process
	// (G-ENGINE-6); after a Commit the stage is closed and this is a no-op
	defer engine.Discard(sctx)
	stopped := func(werr error, at string) error {
		return errors.Join(werr, errs.New(errs.ResourceInvalid, "resource import: "+at+" failed; nothing written (all or nothing)"))
	}
	for _, p := range plan {
		rctx := engine.WithImport(sctx, origin.Import{Source: format, ExtID: p.extID})
		sets := append(slices.Clone(p.first), p.later...)
		var got RowOut
		var werr error
		if id, up := existing[p.extID]; up {
			got, werr = o.resEdit(rctx, ResEditIn{Name: id, Set: sets})
			out.Updated++
		} else {
			got, werr = o.resAdd(rctx, ResAddIn{Kind: p.kind, Name: p.name, Set: sets, To: in.To})
			out.Created++
		}
		if werr != nil {
			return ResImportOut{}, stopped(werr, p.extID)
		}
		for j := range out.Rows {
			if out.Rows[j].Kind == p.kind && out.Rows[j].Name == p.name {
				out.Rows[j].ID = got.ID
			}
		}
	}
	if _, err = engine.Commit(sctx); err != nil {
		return ResImportOut{}, stopped(err, "the batch write")
	}
	return out, nil
}

// pendingRes is a row an import is about to create (FB-RES-1): its pinned
// id, kind id and name, so refs to it resolve before it is written.
type pendingRes struct {
	id     ids.ID[ids.Resource]
	kindID string
	name   string
	src    store.Source
}

// pendingKey is the context key of the import's pending rows.
type pendingKey struct{}

// withPending marks ctx as an import whose new rows are pend.
func withPending(ctx context.Context, pend []pendingRes) context.Context {
	return context.WithValue(ctx, pendingKey{}, pend)
}

// pendingOf pins an id for every record of plan the import creates (not
// the upserts in existing, which already have one).
func (o ops) pendingOf(ctx context.Context, target store.Source, plan []plannedRecord, existing map[string]string) ([]pendingRes, error) {
	out := make([]pendingRes, 0, len(plan))
	for _, p := range plan {
		if _, up := existing[p.extID]; up {
			continue
		}
		d, err := o.kindDef(ctx, p.kind)
		if err != nil {
			return nil, err
		}
		id, err := ids.New[ids.Resource]()
		if err != nil {
			return nil, err
		}
		out = append(out, pendingRes{id: id, kindID: d.kind.Row.ID.String(), name: p.name, src: target})
	}
	return out, nil
}

// pendingRow finds a pending row by any name the shared resolver takes
// (ops/internal/resname: bare, <kind>/<name>, <place>:<kind>/<name>, id;
// and kind id when given): the import's not yet written rows read as
// written for ref resolution. Two pending rows sharing the name, with no
// kind to pick one, is E_RESOURCE_AMBIGUOUS naming the qualified names,
// the same refusal as two stored rows.
func pendingRow(ctx context.Context, key, kindID string, kindName func(id string) string) (located, bool, error) {
	pend, _ := ctx.Value(pendingKey{}).([]pendingRes)
	forms := resname.Forms(key) // RES 3.4: the same names as every resource lookup
	var hits []pendingRes
	for _, p := range pend {
		c := resname.Candidate{ID: p.id.String(), KindID: p.kindID, Name: p.name, Src: p.src}
		if (kindID == "" || p.kindID == kindID) && resname.Matches(c, key, forms, kindName) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return located{}, false, nil
	case 1:
	default:
		cs := make([]resname.Candidate, len(hits))
		for i, p := range hits {
			cs[i] = resname.Candidate{ID: p.id.String(), KindID: p.kindID, Name: p.name, Src: p.src}
		}
		return located{}, true, resname.Ambiguous(key, cs, kindName)
	}
	p := hits[0]
	kid, err := ids.ParseAny(p.kindID)
	if err != nil {
		return located{}, true, errs.Wrap(errs.Internal, "resource import: kind id "+p.kindID, err)
	}
	kv, err := ref(kid)
	if err != nil {
		return located{}, true, err
	}
	nv, err := txt(p.name)
	if err != nil {
		return located{}, true, err
	}
	r := row.Row{Table: schema.TableResources, ID: p.id.Any(), Fields: map[string]value.Value{rKind: kv, rName: nv}}
	return located{Src: p.src, Row: r}, true, nil
}

// pinnedID is the id the running import pinned for a new (kind, name).
func pinnedID(ctx context.Context, kindID, name string) (ids.AnyID, bool) {
	pend, _ := ctx.Value(pendingKey{}).([]pendingRes)
	for _, p := range pend {
		if p.kindID == kindID && p.name == name {
			return p.id.Any(), true
		}
	}
	return ids.AnyID{}, false
}

// createRes writes a new resource row: under the id its import pinned
// (FB-RES-1, engine.WithCreateID), else a fresh one. It goes through
// Env.Create either way, so the wire guards of a plain create (leak guard,
// default target) apply to an imported row too.
func (o ops) createRes(ctx context.Context, target store.Source, kindID, name string, fields map[string]value.Value) (engine.Written, error) {
	if id, pinned := pinnedID(ctx, kindID, name); pinned {
		ctx = engine.WithCreateID(ctx, id)
	}
	return o.env.Create(ctx, target, schema.TableResources, fields)
}

// existingIn finds a live resource (kind, name) in target.
func (o ops) existingIn(ctx context.Context, target store.Source, r resRecord) (located, bool) {
	d, err := o.kindDef(ctx, r.kind)
	if err != nil {
		return located{}, false
	}
	rows, err := liveRows(ctx, o.env, []store.Source{target}, schema.TableResources)
	if err != nil {
		return located{}, false
	}
	if hit := where(rows, map[string]string{rKind: d.kind.Row.ID.String(), rName: r.name}); len(hit) > 0 {
		return hit[0], true
	}
	return located{}, false
}

// ResExportIn is the input of resource.export.
type ResExportIn struct {
	Kind          string   `json:"kind" flag:"kind" desc:"the kind to export (name or knd_ id)"`
	Format        string   `json:"format,omitempty" flag:"format" desc:"csv, json (default) or yaml"`
	Where         []string `json:"where,omitempty" flag:"where" desc:"filter on a field, as resource list --where (repeatable)"`
	ShowSensitive bool     `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"export sensitive values (asks first); masked as ••• otherwise"`
}

// ResExportOut is the output of resource.export: the file content.
type ResExportOut struct {
	Format  string `json:"format"`
	Count   int    `json:"count"`
	Content string `json:"content"`
	// Notes: the last good definition warning of a conflicted kind (RES
	// 3.13); JSON only, because Text is the file itself.
	Notes []string `json:"notes,omitempty"`
}

// Text implements op.Texter: the file itself.
func (o ResExportOut) Text() string { return o.Content }

func (o ops) resExport(ctx context.Context, in ResExportIn) (ResExportOut, error) {
	format := FormatJSON
	if in.Format != "" {
		f, err := formatOf(in.Format, "")
		if err != nil {
			return ResExportOut{}, err
		}
		format = f
	}
	list, err := o.resList(ctx, ResListIn{Kind: in.Kind, Where: in.Where, ShowSensitive: in.ShowSensitive})
	if err != nil {
		return ResExportOut{}, err
	}
	d, err := o.kindDefRead(ctx, in.Kind)
	if err != nil {
		return ResExportOut{}, err
	}
	out := ResExportOut{Format: format, Count: list.Count, Notes: d.noteLines()}
	switch format {
	case FormatCSV:
		var b bytes.Buffer
		w := csv.NewWriter(&b)
		cols := d.fieldNames()
		if err = w.Write(append([]string{csvName}, cols...)); err != nil {
			return ResExportOut{}, errs.Wrap(errs.Internal, "resource export: csv", err)
		}
		for _, r := range list.Data {
			cells := []string{r.Name}
			for _, c := range cols {
				cells = append(cells, r.Values[c])
			}
			if err = w.Write(cells); err != nil {
				return ResExportOut{}, errs.Wrap(errs.Internal, "resource export: csv", err)
			}
		}
		w.Flush()
		out.Content = b.String()
	default:
		doc := struct {
			Resources []exportElem `json:"resources" yaml:"resources"`
		}{Resources: []exportElem{}}
		for _, r := range list.Data {
			doc.Resources = append(doc.Resources, exportElem{Kind: r.Kind, Name: r.Name, Fields: r.Values})
		}
		var b []byte
		if format == FormatYAML {
			b, err = yaml.Marshal(doc)
		} else {
			b, err = json.MarshalIndent(doc, "", "  ")
			b = append(b, '\n')
		}
		if err != nil {
			return ResExportOut{}, errs.Wrap(errs.Internal, "resource export: encode", err)
		}
		out.Content = string(b)
	}
	return out, nil
}

// exportElem is one exported resource (values as display text, refs as names).
type exportElem struct {
	Kind   string            `json:"kind" yaml:"kind"`
	Name   string            `json:"name" yaml:"name"`
	Fields map[string]string `json:"fields" yaml:"fields"`
}

// ResRepointIn is the input of resource.repoint.
type ResRepointIn struct {
	Old    string   `json:"old" arg:"0" desc:"the resource refs point at now (name or res_ id)"`
	New    string   `json:"new" arg:"1" desc:"the resource they should point at (same kind)"`
	Fields []string `json:"fields,omitempty" flag:"fields" desc:"only these ref fields (comma list or repeatable)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"list the refs that would move; write nothing"`
}

// ResRepointOut is the output of resource.repoint.
type ResRepointOut struct {
	Batch  string   `json:"batch,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	Count  int      `json:"count"`
	Rows   []string `json:"rows"`
	Human  string   `json:"-"`
}

// Text implements op.Texter.
func (o ResRepointOut) Text() string { return o.Human }

func (o ops) resRepoint(ctx context.Context, in ResRepointIn) (ResRepointOut, error) {
	oldR, err := o.resourceByName(ctx, in.Old, "")
	if err != nil {
		return ResRepointOut{}, err
	}
	newR, err := o.resourceByName(ctx, in.New, text(oldR.Row, rKind))
	if err != nil {
		// OV33/RES 3.6: a live resource of another kind is a kind mismatch,
		// not an invalid value.
		if anyR, aerr := o.resourceByName(ctx, in.New, ""); aerr == nil && text(anyR.Row, rKind) != text(oldR.Row, rKind) {
			return ResRepointOut{}, errs.New(errs.ResourceKindMismatch, "resource repoint: "+in.New+" is a "+o.kindName(ctx, text(anyR.Row, rKind))+
				", "+in.Old+" is a "+o.kindName(ctx, text(oldR.Row, rKind))+"; refs can only move to a resource of the same kind; nothing written")
		}
		return ResRepointOut{}, errs.Wrap(errs.ResourceInvalid, "resource repoint: "+in.New+" must be a live resource of the same kind as "+in.Old, err)
	}
	if text(newR.Row, rArchivedAt) != "" {
		return ResRepointOut{}, errs.New(errs.ResourceArchived, "resource repoint: "+in.New+" is archived").WithHint("repoint to the resource that replaced it (its replaced_by)")
	}
	only := splitList(strings.Join(in.Fields, ","))
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return ResRepointOut{}, err
	}
	type change struct {
		r     located
		d     kindDef
		store resValues
		label string
	}
	var changes []change
	oldID, newRaw := oldR.Row.ID.String(), json.RawMessage(quote(newR.Row.ID.String()))
	for _, r := range rows {
		d, derr := o.kindDef(ctx, text(r.Row, rKind))
		if derr != nil {
			continue // a row of a kind this place cannot see
		}
		stored, derr := d.decode(r)
		if derr != nil {
			return ResRepointOut{}, derr
		}
		var hit []string
		for id, raw := range stored.vals {
			f, ok := d.byID[id]
			if !ok || text(f.Row, fType) != string(value.KindRef) || display(fromJSONRaw(raw)) != oldID {
				continue
			}
			if len(only) > 0 && !slices.Contains(only, text(f.Row, kName)) {
				continue
			}
			stored.vals[id] = newRaw
			hit = append(hit, text(f.Row, kName))
		}
		if len(hit) > 0 {
			slices.Sort(hit)
			changes = append(changes, change{r: r, d: d, store: stored, label: d.name() + "/" + text(r.Row, rName) + " " + strings.Join(hit, ",")})
		}
	}
	out := ResRepointOut{DryRun: in.DryRun, Count: len(changes), Rows: []string{}}
	for _, c := range changes {
		out.Rows = append(out.Rows, c.label)
	}
	var b strings.Builder
	verb := "repointed"
	if in.DryRun {
		verb = "would repoint"
	}
	fmt.Fprintf(&b, "%s %s from %s to %s:\n", verb, plural.Count(len(changes), "row"), text(oldR.Row, rName), text(newR.Row, rName))
	for _, l := range out.Rows {
		b.WriteString("  " + l + "\n")
	}
	if in.DryRun {
		out.Human = b.String() + "then archive " + text(oldR.Row, rName) + "; nothing written (dry run)\n"
		return out, nil
	}
	if err = bulk.Ask(ctx, "repoint "+plural.Count(len(changes), "row")+" from "+text(oldR.Row, rName)+" to "+text(newR.Row, rName)+" and archive "+text(oldR.Row, rName)+"?"); err != nil {
		return ResRepointOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindResource)
	if err != nil {
		return ResRepointOut{}, err
	}
	out.Batch = bid.String()
	for _, c := range changes {
		patch, perr := o.valuesPatch(ctx, c.d, c.store.vals, c.store)
		if perr != nil {
			return ResRepointOut{}, perr
		}
		if perr = o.updateResource(bctx, c.d, c.r, patch); perr != nil {
			return ResRepointOut{}, errors.Join(perr, errs.New(errs.ResourceInvalid, "resource repoint: stopped; the rows written are in batch "+out.Batch))
		}
	}
	// RESOURCES 3.20: the old row is archived with replaced_by <new id>, so
	// show --archived names its successor.
	succ, err := ref(newR.Row.ID)
	if err != nil {
		return ResRepointOut{}, err
	}
	if _, err = o.patchRow(bctx, oldR, map[string]value.Value{rArchivedAt: value.DateTimeFrom(opclock.Now(o.env)), rReplacedBy: succ}, "archived "+text(oldR.Row, rName)); err != nil {
		return ResRepointOut{}, err
	}
	out.Human = b.String() + "archived " + text(oldR.Row, rName) + " (replaced_by " + text(newR.Row, rName) + "); batch " + out.Batch + "\n"
	return out, nil
}

// ResCloneIn is the input of resource.clone.
type ResCloneIn struct {
	From string   `json:"from" arg:"0" desc:"the resource to copy (name or res_ id)"`
	Name string   `json:"name" arg:"1" desc:"the new resource's name"`
	Set  []string `json:"set,omitempty" flag:"set" desc:"field=value overriding a copied value (repeatable)"`
	To   string   `json:"to,omitempty" flag:"to" desc:"where the new row is written (default repo)"`
}

func (o ops) resClone(ctx context.Context, in ResCloneIn) (RowOut, error) {
	src, err := o.resourceByName(ctx, in.From, "")
	if err != nil {
		return RowOut{}, err
	}
	d, err := o.kindDef(ctx, text(src.Row, rKind))
	if err != nil {
		return RowOut{}, err
	}
	stored, err := d.decode(src)
	if err != nil {
		return RowOut{}, err
	}
	over := map[string]bool{}
	for _, s := range in.Set {
		f, _, _ := strings.Cut(s, setSep)
		over[f] = true
	}
	var sets []string
	for name, v := range o.named(ctx, d, stored.vals, true) {
		// The copy goes through resAdd's checks as CLI text; a ref copies
		// as its target's name, a list as its items.
		if !over[name] {
			if f, ok := d.byName[name]; ok && text(f.Row, fType) == string(value.KindList) {
				b, merr := json.Marshal(value.ItemStrings(fromJSONRaw(stored.vals[f.Row.ID.String()])))
				if merr != nil {
					return RowOut{}, errs.Wrap(errs.Internal, "resource clone: encode "+name, merr)
				}
				v = string(b)
			}
			sets = append(sets, name+setSep+v)
		}
	}
	slices.Sort(sets)
	return o.resAdd(ctx, ResAddIn{Kind: d.kind.Row.ID.String(), Name: in.Name, Set: append(sets, in.Set...), To: in.To})
}

// nodeJSON re-encodes a YAML node as JSON, typed by the node's resolved
// tag (so the import shares the JSON field decoder without an untyped
// intermediate value).
func nodeJSON(n *yaml.Node) ([]byte, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 1 {
			return nodeJSON(n.Content[0])
		}
	case yaml.AliasNode:
		if n.Alias != nil {
			return nodeJSON(n.Alias)
		}
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int", "!!float":
			if json.Valid([]byte(n.Value)) {
				return []byte(n.Value), nil
			}
		case "!!bool":
			if n.Value == "true" || n.Value == "false" {
				return []byte(n.Value), nil
			}
		case "!!null":
			return []byte("null"), nil
		}
		return json.Marshal(n.Value)
	case yaml.SequenceNode:
		items := make([]json.RawMessage, 0, len(n.Content))
		for _, c := range n.Content {
			b, err := nodeJSON(c)
			if err != nil {
				return nil, err
			}
			items = append(items, b)
		}
		return json.Marshal(items)
	case yaml.MappingNode:
		m := make(map[string]json.RawMessage, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			b, err := nodeJSON(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = b
		}
		return json.Marshal(m)
	}
	return nil, errs.New(errs.InvalidInput, "resource import: an unsupported yaml value at line "+strconv.Itoa(n.Line))
}

// batchRefusal is the E_RESOURCE_BATCH refusal of an import: the printed
// message names the row's own reason (the envelope carries only the
// message, never the wrapped cause) and keeps the cause's hint, so a
// person can fix the row without re-running with --skip-errors to learn
// what was wrong.
func batchRefusal(rowLabel, tail string, cause error) error {
	body := errs.EnvelopeOf(cause).Error
	msg := body.Message
	if rowLabel != "" && !strings.HasPrefix(msg, rowLabel) { // most row errors name the row already
		msg = rowLabel + ": " + msg
	}
	e := errs.Wrap(errs.ResourceBatch, "resource import: "+msg+"; "+tail, cause)
	if body.Hint != "" {
		e = e.WithHint(body.Hint)
	}
	return e
}
