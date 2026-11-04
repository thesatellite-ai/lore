package settings

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
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
)

// Metadata export and import (METADATA 3.19): CSV with the columns
// id,key,value, or JSON lines {"id","key","value"}. A text value is written
// as itself; any other value (a number, a list, an object) as its JSON
// text, read back with --meta-json semantics. Sensitive values are masked
// on export and a masked value is refused on import (it would overwrite
// the real value with the mask).
const (
	OpMetaExport = "meta.export"
	OpMetaImport = "meta.import"

	metaFormatCSV  = "csv"
	metaFormatJSON = "json"
	// maxMetaImportBytes caps an import file.
	maxMetaImportBytes = 16 << 20
)

// metaCSVHeader is the CSV header row.
var metaCSVHeader = []string{"id", "key", "value"}

// MetaExportIn is the input of meta.export.
type MetaExportIn struct {
	Table  string `json:"table,omitempty" flag:"table" desc:"only rows of this table"`
	Keys   string `json:"keys,omitempty" flag:"key" desc:"only these keys, comma separated"`
	Format string `json:"format,omitempty" flag:"format" desc:"csv (default) or json (one object per line)"`
}

// MetaRecord is one exported or imported value.
type MetaRecord struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MetaExportOut is the output of meta.export.
type MetaExportOut struct {
	Format string       `json:"format"`
	Count  int          `json:"count"`
	Data   []MetaRecord `json:"data"`
	text   string
}

// Text implements op.Texter: the file content itself, for `> file`.
func (o MetaExportOut) Text() string { return o.text }

// MetaImportIn is the input of meta.import.
type MetaImportIn struct {
	File   string `json:"file" arg:"0" desc:"the file to import (csv or json lines)"`
	Format string `json:"format,omitempty" flag:"format" desc:"csv or json (default: from the file extension)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would be written; write nothing"`
}

func (o ops) metaIOOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpMetaExport, Summary: "Export metadata values as CSV or JSON lines (id,key,value)", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, RESTNoun: metaREST}, o.metaExport),
		op.Define(op.Spec{Name: OpMetaImport, Summary: "Import metadata values from CSV or JSON lines, as one batch", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: metaWriteErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST}, o.metaImport),
	}
}

// metaFormat picks csv or json from the flag or the file extension.
func metaFormat(given, file string) (string, error) {
	f := strings.ToLower(given)
	if f == "" && file != "" {
		f = strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
	}
	switch f {
	case "", metaFormatCSV:
		return metaFormatCSV, nil
	case metaFormatJSON, "jsonl", "ndjson":
		return metaFormatJSON, nil
	}
	return "", errs.New(errs.InvalidInput, "meta: unknown format "+quote(f)).WithHint("--format csv or json")
}

// exportValue is one entry's value as a record value.
func exportValue(en meta.Entry) string {
	if en.Sensitive {
		return maskedValue
	}
	raw := en.V
	if len(en.VName) > 0 {
		raw = en.VName
	}
	if canon.KindOf(raw) == canon.KindString {
		if s := meta.Strings(raw); len(s) == 1 {
			return s[0]
		}
	}
	return string(raw)
}

// metaExport is `lore meta export` (3.19): every value of every row, sorted
// by row id then key.
func (o ops) metaExport(ctx context.Context, in MetaExportIn) (MetaExportOut, error) {
	format, err := metaFormat(in.Format, "")
	if err != nil {
		return MetaExportOut{}, err
	}
	rows, err := o.metaRows(ctx)
	if err != nil {
		return MetaExportOut{}, err
	}
	keys := splitList(in.Keys)
	out := MetaExportOut{Format: format, Data: []MetaRecord{}}
	for _, r := range rows {
		if in.Table != "" && string(r.Row.Table) != in.Table {
			continue
		}
		for _, en := range r.entries {
			if len(keys) > 0 && !slices.Contains(keys, en.Name) {
				continue
			}
			out.Data = append(out.Data, MetaRecord{ID: r.Row.ID.String(), Key: en.Name, Value: exportValue(en)})
		}
	}
	slices.SortFunc(out.Data, func(a, b MetaRecord) int { return strings.Compare(a.ID+"\x00"+a.Key, b.ID+"\x00"+b.Key) })
	out.Count = len(out.Data)
	var b bytes.Buffer
	switch format {
	case metaFormatJSON:
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		for _, rec := range out.Data {
			if err := enc.Encode(rec); err != nil {
				return MetaExportOut{}, errs.Wrap(errs.Internal, "meta export", err)
			}
		}
	default:
		w := csv.NewWriter(&b)
		if err := w.Write(metaCSVHeader); err != nil {
			return MetaExportOut{}, errs.Wrap(errs.Internal, "meta export", err)
		}
		for _, rec := range out.Data {
			if err := w.Write([]string{rec.ID, rec.Key, rec.Value}); err != nil {
				return MetaExportOut{}, errs.Wrap(errs.Internal, "meta export", err)
			}
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return MetaExportOut{}, errs.Wrap(errs.Internal, "meta export", err)
		}
	}
	out.text = b.String()
	return out, nil
}

// parseMetaImport reads the records of an import file.
func parseMetaImport(format string, raw []byte) ([]MetaRecord, error) {
	var recs []MetaRecord
	if format == metaFormatJSON {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		for line := 1; ; line++ {
			var r MetaRecord
			if err := dec.Decode(&r); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, errs.Wrap(errs.InvalidInput, fmt.Sprintf("meta import: record %d", line), err)
			}
			recs = append(recs, r)
		}
		return recs, nil
	}
	rd := csv.NewReader(bytes.NewReader(raw))
	all, err := rd.ReadAll()
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "meta import: csv", err)
	}
	if len(all) == 0 || !slices.Equal(all[0], metaCSVHeader) {
		return nil, errs.New(errs.InvalidInput, "meta import: the first csv row must be id,key,value")
	}
	for _, rec := range all[1:] {
		recs = append(recs, MetaRecord{ID: rec[0], Key: rec[1], Value: rec[2]})
	}
	return recs, nil
}

// importTerm turns a record value into a write: text as --meta text, a
// JSON number/list/object/bool as --meta-json (so an export re-imports to
// the same value).
func importTerm(rec MetaRecord) (meta.Term, bool, error) {
	t, err := meta.ParseWrite(rec.Key + "=" + rec.Value)
	if err != nil {
		return meta.Term{}, false, err
	}
	switch canon.KindOf([]byte(rec.Value)) {
	case canon.KindNumber, canon.KindArray, canon.KindObject, canon.KindBool:
		return t, true, nil
	case canon.KindInvalid, canon.KindNull, canon.KindString:
	}
	return t, false, nil
}

// metaImport is `lore meta import <file>` (3.19). Flow: read + parse every
// record (any bad record, a masked value or a bad id refuses the whole file
// before the first write) ─► group by row ─► dry run lists ─► preview above
// batch.preview_rows (XQ2) ─► one import batch; every write runs the normal
// metadata checks, so `lore batch undo` reverses it.
func (o ops) metaImport(ctx context.Context, in MetaImportIn) (RowOut, error) {
	format, err := metaFormat(in.Format, in.File)
	if err != nil {
		return RowOut{}, err
	}
	raw, err := readMetaImport(in.File)
	if err != nil {
		return RowOut{}, err
	}
	recs, err := parseMetaImport(format, raw)
	if err != nil {
		return RowOut{}, err
	}
	writes := map[ids.AnyID]*engine.MetaWrite{}
	var order []ids.AnyID
	for i, rec := range recs {
		if rec.Value == maskedValue {
			return RowOut{}, errs.New(errs.MetaValue, fmt.Sprintf("meta import: record %d (%s %s) holds the mask %s of a sensitive value; it would overwrite the real value", i+1, rec.ID, rec.Key, maskedValue)).
				WithHint("drop the record, or give the real value")
		}
		id, ierr := rowID(rec.ID)
		if ierr != nil {
			return RowOut{}, ierr
		}
		t, isJSON, terr := importTerm(rec)
		if terr != nil {
			return RowOut{}, errs.Wrap(errs.CodeOf(terr), fmt.Sprintf("meta import: record %d", i+1), terr)
		}
		mw := writes[id]
		if mw == nil {
			mw = &engine.MetaWrite{ID: id}
			writes[id] = mw
			order = append(order, id)
		}
		if isJSON {
			mw.JSON = append(mw.JSON, t)
		} else {
			mw.Set = append(mw.Set, t)
		}
	}
	if len(order) == 0 {
		return RowOut{Human: "meta import: no records; nothing written\n"}, nil
	}
	if in.DryRun {
		return RowOut{Human: fmt.Sprintf("would import %d values on %d rows from %s\ndry run: nothing written\n", len(recs), len(order), in.File)}, nil
	}
	items := make([]bulk.Item, 0, len(order))
	for _, id := range order {
		items = append(items, bulk.Item{ID: id.String()})
	}
	srcs := o.env.Stores()
	if len(srcs) > 0 {
		if err = bulk.Preview(ctx, o.env, srcs[0], "meta import", items); err != nil {
			return RowOut{}, err
		}
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindImport)
	if err != nil {
		return RowOut{}, err
	}
	for i, id := range order {
		if _, err := o.env.Update(engine.WithMeta(bctx, *writes[id]), engine.Ref{ID: id}, map[string]value.Value{}); err != nil {
			return RowOut{}, errs.Wrap(errs.CodeOf(err), fmt.Sprintf("meta import: stopped at row %s after %d of %d rows (batch %s; undo: lore batch undo %s)", id, i, len(order), bid, bid), err)
		}
	}
	return RowOut{ID: bid.String(), Human: fmt.Sprintf("imported %d values on %d rows; batch %s (undo: lore batch undo %s)\n", len(recs), len(order), bid, bid)}, nil
}

// readMetaImport reads an import file, capped.
func readMetaImport(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errs.New(errs.InvalidInput, "meta import: name the file to import")
	}
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "meta import: open "+path, err)
	}
	defer func() { _ = fh.Close() }() // read only
	b, err := io.ReadAll(io.LimitReader(fh, maxMetaImportBytes+1))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "meta import: read "+path, err)
	}
	if len(b) > maxMetaImportBytes {
		return nil, errs.New(errs.InvalidInput, "meta import: "+path+" is too large")
	}
	return b, nil
}
