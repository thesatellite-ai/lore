package renderhost

// `lore context --internal` over the core (P3-CMDLORE2): the rows of the
// attached internal stores (the personal store and every store whose
// lore-store.yaml does not read visibility: shareable), which never render
// into a file inside the checkout. The old binary passes its v0.1.17 store
// cache reader as Options.External instead; cmd/lore passes CoreExternal.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// InternalRow is one `lore context --internal --json` element: the row's
// own fields flattened beside its id, table, `_source` (the same object
// every core list prints) and `_label` (the "[store]" shown beside it).
// Metadata is left out (MQ13: sensitive values never leave a list), and
// v0.1.17's `_live_row` has no core counterpart (live rows are ordinary
// rows of the repo store, which --internal never reads).
type InternalRow struct {
	ID     string
	Table  row.Table
	Fields map[string]value.Value
	Source store.SourceRef
	Label  string
}

// The reserved keys of InternalRow's JSON object. A row field of the same
// name loses to them (a field can never be named with a leading "_", and
// "id"/"table" are the envelope's own).
const (
	internalKeyID     = "id"
	internalKeyTable  = "table"
	internalKeySource = "_source"
	internalKeyLabel  = "_label"
)

// MarshalJSON writes the flat object: the fields first, then the reserved
// keys over them. encoding/json sorts map keys, so output is stable.
func (r InternalRow) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(r.Fields)+4)
	for k, v := range r.Fields {
		if v == nil {
			continue
		}
		b, err := v.MarshalJSON()
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	for k, v := range map[string]string{internalKeyID: r.ID, internalKeyTable: string(r.Table), internalKeyLabel: r.Label} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	src, err := json.Marshal(r.Source)
	if err != nil {
		return nil, err
	}
	out[internalKeySource] = src
	return json.Marshal(out)
}

// internalTables are the tables --internal reads: the private render's
// (render_private.go), so --internal shows exactly what the out-of-checkout
// render could, and never a resource table whose fields may be sensitive.
func internalTables() []row.Table {
	out := make([]row.Table, len(privateTables))
	for i, t := range privateTables {
		out[i] = t.table
	}
	return out
}

// InternalRows lists the live, not archived rows of the attached internal
// stores (table "" = every internalTables table; text "" = all, else a
// case-insensitive substring of the row's title, name, body or
// description), ordered by store then id.
func InternalRows(ctx context.Context, app *wire.App, table, text string) ([]InternalRow, error) {
	if app == nil {
		return nil, errs.New(errs.Internal, "the core is not mounted")
	}
	tables := internalTables()
	if table != "" {
		if !slices.Contains(tables, row.Table(table)) {
			names := make([]string, len(tables))
			for i, t := range tables {
				names[i] = string(t)
			}
			return nil, errs.New(errs.InvalidInput, fmt.Sprintf("context --internal --table %q: not a table --internal reads", table)).
				WithHint("one of: " + strings.Join(names, ", "))
		}
		tables = []row.Table{row.Table(table)}
	}
	idents, err := app.StoreIdentities(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(text)
	var out []InternalRow
	for _, si := range idents {
		if si.Shareable {
			continue
		}
		label := "[" + app.StoreLabel(si.Name) + "]"
		ref := app.DescribeSource(si.Name)
		var rows []InternalRow
		for _, t := range tables {
			list, lerr := app.List(ctx, si.Name, t)
			if lerr != nil {
				return nil, lerr
			}
			for _, r := range list {
				if r.Deleted || renderField(r, renderFieldArchivedAt) != "" {
					continue
				}
				if needle != "" && !rowMatches(r, needle) {
					continue
				}
				rows = append(rows, InternalRow{ID: r.ID.String(), Table: r.Table, Fields: r.Fields, Source: ref, Label: label})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		out = append(out, rows...)
	}
	return out, nil
}

// rowMatches reports whether the lowered needle is in any text field
// --internal prints.
func rowMatches(r row.Row, needle string) bool {
	for _, f := range []string{renderFieldTitle, privateFieldName, renderFieldBody, privateFieldDescription} {
		if strings.Contains(strings.ToLower(renderField(r, f)), needle) {
			return true
		}
	}
	return false
}

// CoreExternal is Options.External over the core: one line per row ("<id>
// <title — body>  <status>  [<store>]", v0.1.17's shape) and the
// []InternalRow JSON.
func CoreExternal(app *wire.App) func(ctx context.Context, w *strings.Builder, table, text string) (int, []byte, error) {
	return func(ctx context.Context, w *strings.Builder, table, text string) (int, []byte, error) {
		rows, err := InternalRows(ctx, app, table, text)
		if err != nil {
			return 0, nil, err
		}
		for _, r := range rows {
			status := ""
			if v, ok := r.Fields[privateFieldStatus]; ok && v != nil && v.String() != "" {
				status = "  " + v.String()
			}
			fmt.Fprintf(w, "%s  %s%s  %s\n", r.ID, oneLine(privateRowText(row.Row{Fields: r.Fields})), status, r.Label)
		}
		if rows == nil {
			rows = []InternalRow{}
		}
		b, err := json.Marshal(rows)
		return len(rows), b, err
	}
}
