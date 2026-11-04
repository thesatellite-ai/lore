package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Field names the cache reads from settings and resource rows (schema keeps
// its own copies unexported; these are the stored contract of spec 4.4).
const (
	fieldName    = "name"    // kind_fields.name
	fieldKindID  = "kind_id" // kind_fields.kind_id
	fieldResKind = "kind"    // resources.kind
	fieldResVals = "fields"  // resources.fields: {fld_id: {name, v}}
	resValueKey  = "v"       // the value inside one resources.fields entry
)

// project brings the typed tables in line with one applied row.
func (c *Cache) project(ctx context.Context, tx *sql.Tx, st state, source store.Source, sr store.Row) error {
	p, known := st.plans[sr.Table]
	isResource := sr.Table == schema.TableResources
	if !known && !isResource {
		return nil // unknown table: kept in _rows only
	}
	if isResource {
		for _, k := range st.kinds {
			if err := c.deleteTyped(ctx, tx, st.plans[k.table], string(source), sr.ID); err != nil {
				return err
			}
		}
	}
	if sr.Deleted {
		if known {
			return c.deleteTyped(ctx, tx, p, string(source), sr.ID)
		}
		return nil
	}
	r, err := sr.Decode()
	if err != nil {
		// A document this binary cannot read (a newer _v) stays in _rows
		// only; a newer lore will project it.
		if known {
			return c.deleteTyped(ctx, tx, p, string(source), sr.ID)
		}
		return nil
	}
	if known {
		if err := c.upsertTyped(ctx, tx, p, string(source), r, r.Fields); err != nil {
			return err
		}
	}
	if isResource {
		return c.upsertKind(ctx, tx, st, kindOf(r), string(source), r)
	}
	return nil
}

// upsertKind projects resource r into its kind's table, if the kind is
// known. Values are read by fld_ id; a value that does not parse as its
// field's kind is kept as inferred (the cache mirrors, the validator
// judges).
func (c *Cache) upsertKind(ctx context.Context, tx *sql.Tx, st state, kind ids.ID[ids.Kind], source string, r row.Row) error {
	info, ok := st.kinds[kind]
	if kind.IsZero() || !ok {
		return nil
	}
	vals := map[string]value.Value{}
	if v, ok := r.Fields[fieldName]; ok {
		vals[fieldName] = v
	}
	if raw, ok := r.Fields[fieldResVals].(value.JSON); ok && raw.IsObject() {
		members, err := canon.Members(raw.Raw())
		if err != nil {
			return errs.Wrap(errs.DBCorrupt, "sqlite cache: resource "+r.ID.String()+" fields", err)
		}
		for _, m := range members {
			f, known := info.fields[m.Key]
			if !known {
				continue
			}
			v, ok := entryValue(f, m.Raw)
			if ok {
				vals[f.Name] = v
			}
		}
	}
	return c.upsertTyped(ctx, tx, st.plans[info.table], source, r, vals)
}

// entryValue reads the "v" of one resources.fields entry as f's kind,
// falling back to the inferred kind.
func entryValue(f schema.FieldDef, entry []byte) (value.Value, bool) {
	members, err := canon.Members(entry)
	if err != nil {
		return nil, false
	}
	for _, m := range members {
		if m.Key != resValueKey {
			continue
		}
		if v, err := value.Parse(f.Kind, m.Raw); err == nil {
			return v, true
		}
		if v, err := value.Infer(m.Raw); err == nil {
			return v, true
		}
		return nil, false
	}
	return nil, false
}

// upsertTyped replaces r's row in p's table (and FTS, and references) with
// vals as the field values (see tableSQL.insert for how values travel).
func (c *Cache) upsertTyped(ctx context.Context, tx *sql.Tx, p plan, source string, r row.Row, vals map[string]value.Value) error {
	cells := make([]json.RawMessage, 0, len(p.columns))
	for _, col := range p.columns {
		cells = append(cells, cellFor(col, source, r, vals))
	}
	arr, err := json.Marshal(cells)
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite cache: encode row", err)
	}
	if _, err = tx.ExecContext(ctx, p.sql.insert, string(arr)); err != nil {
		return dbErr("upsert "+p.def.Name.String(), err)
	}
	if err := upsertFTS(ctx, tx, p, source, r.ID, vals); err != nil {
		return err
	}
	if err := fillJoins(ctx, tx, p, source, r.ID, vals); err != nil {
		return err
	}
	return c.refreshRefs(ctx, tx, p, r.ID, vals)
}

// fillJoins replaces the pairs of one row in every join table of p from
// its list-of-ref fields (decision O11). An item that is not a valid id
// (an old or hand-edited row) is skipped: the cache mirrors, the validator
// reports it.
func fillJoins(ctx context.Context, tx *sql.Tx, p plan, source string, id ids.AnyID, vals map[string]value.Value) error {
	for _, j := range p.joins {
		if _, err := tx.ExecContext(ctx, j.deleteFrom, source, id.String()); err != nil {
			return dbErr("clear pairs of "+j.rel.JoinTable.String(), err)
		}
		list, ok := vals[j.rel.Field].(value.List)
		if !ok {
			continue
		}
		for _, item := range list.Items() {
			to, err := ids.ParseAny(item.String())
			if err != nil {
				continue
			}
			if _, err := tx.ExecContext(ctx, j.insert, source, id.String(), to.String()); err != nil {
				return dbErr("record pair in "+j.rel.JoinTable.String(), err)
			}
		}
	}
	return nil
}

// extractList returns "json_extract(?1, '$[0]'), …" for n columns.
func extractList(n int) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = "json_extract(?1, '$[" + strconv.Itoa(i) + "]')"
	}
	return strings.Join(parts, ", ")
}

// jsonNull is the JSON null cell.
var jsonNull = json.RawMessage("null")

// cellFor is the JSON cell of one column.
func cellFor(col column, source string, r row.Row, vals map[string]value.Value) json.RawMessage {
	switch col.name {
	case colID:
		return canon.String(r.ID.String())
	case colSource:
		return canon.String(source)
	case colHLC:
		return canon.String(r.HLC.String())
	case row.KeyCreatedAt:
		return timeCell(r.CreatedAt)
	case row.KeyUpdatedAt:
		return timeCell(r.UpdatedAt)
	case row.KeyCreatedBy:
		return idCell(r.CreatedBy.Any())
	case row.KeyUpdatedBy:
		return idCell(r.UpdatedBy.Any())
	}
	return valueCell(vals[col.name])
}

// timeCell renders an audit time (zero = NULL).
func timeCell(t time.Time) json.RawMessage {
	if t.IsZero() {
		return jsonNull
	}
	return canon.String(value.DateTimeFrom(t).String())
}

// idCell renders an audit actor (zero = NULL).
func idCell(id ids.AnyID) json.RawMessage {
	if id.IsZero() {
		return jsonNull
	}
	return canon.String(id.String())
}

// valueCell renders a field value per the affinity table of the package
// doc. It switches on the value's own kind, not the column's: a value of
// another kind than declared (an old row) is still stored, never dropped.
func valueCell(v value.Value) json.RawMessage {
	if v == nil {
		return jsonNull
	}
	switch v.Kind() {
	case value.KindNull:
		return jsonNull
	case value.KindBool:
		if b, ok := v.(value.Bool); ok && b.Bool() {
			return json.RawMessage("1")
		}
		return json.RawMessage("0")
	case value.KindNumber:
		return json.RawMessage(v.String())
	case value.KindJSON, value.KindList:
		raw, err := v.MarshalJSON()
		if err != nil {
			return jsonNull
		}
		return canon.String(string(raw))
	case value.KindText, value.KindDate, value.KindDateTime, value.KindDuration,
		value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef:
		return canon.String(v.String())
	}
	return canon.String(v.String())
}

// upsertFTS replaces id's entry in p's FTS table.
func upsertFTS(ctx context.Context, tx *sql.Tx, p plan, source string, id ids.AnyID, vals map[string]value.Value) error {
	if p.fts == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, p.sql.ftsDeleteID, source, id.String()); err != nil {
		return dbErr("fts delete", err)
	}
	cells := []json.RawMessage{canon.String(id.String()), canon.String(source)}
	for _, f := range p.def.FTS {
		cell := jsonNull
		if v, ok := vals[f]; ok && v.Kind() != value.KindNull {
			cell = canon.String(v.String())
		}
		cells = append(cells, cell)
	}
	arr, err := json.Marshal(cells)
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite cache: encode fts row", err)
	}
	if _, err = tx.ExecContext(ctx, p.sql.ftsInsert, string(arr)); err != nil {
		return dbErr("fts insert", err)
	}
	return nil
}

// deleteTyped removes source's copy of id from p's table, FTS and join
// tables, and id's references. References are keyed by id alone, so when
// two sources of a combined file hold the same id (itself an
// E_AMBIGUOUS_ID state) deleting one copy also drops the other's until it
// is applied again.
func (c *Cache) deleteTyped(ctx context.Context, tx *sql.Tx, p plan, source string, id ids.AnyID) error {
	if _, err := tx.ExecContext(ctx, p.sql.deleteID, source, id.String()); err != nil {
		return dbErr("delete", err)
	}
	if p.fts != "" {
		if _, err := tx.ExecContext(ctx, p.sql.ftsDeleteID, source, id.String()); err != nil {
			return dbErr("fts delete", err)
		}
	}
	for _, j := range p.joins {
		if _, err := tx.ExecContext(ctx, j.deleteFrom, source, id.String()); err != nil {
			return dbErr("delete pairs", err)
		}
	}
	for _, q := range []string{
		`DELETE FROM "` + tblRefs + `" WHERE entity_table = ? AND entity_id = ?`,
		`DELETE FROM "` + tblExpanded + `" WHERE entity_table = ? AND entity_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, p.def.Name.String(), id.String()); err != nil {
			return dbErr("delete references", err)
		}
	}
	return nil
}

// clearSource removes every typed row source applied to p's table, with
// its FTS entries and references (references first: they are found
// through the typed rows).
func clearSource(ctx context.Context, tx *sql.Tx, p plan, source store.Source) error {
	for _, q := range []string{p.sql.refsClearSource, p.sql.expandedClearSource} {
		if _, err := tx.ExecContext(ctx, q, p.def.Name.String(), string(source)); err != nil {
			return dbErr("clear references", err)
		}
	}
	if p.fts != "" {
		if _, err := tx.ExecContext(ctx, p.sql.ftsClearSource, string(source)); err != nil {
			return dbErr("clear fts", err)
		}
	}
	for _, j := range p.joins {
		if _, err := tx.ExecContext(ctx, j.clearSource, string(source)); err != nil {
			return dbErr("clear pairs", err)
		}
	}
	if _, err := tx.ExecContext(ctx, p.sql.clearSource, string(source)); err != nil {
		return dbErr("clear rows", err)
	}
	return nil
}

// textFields lists p's text fields in declaration order (the fields whose
// references are tracked).
func textFields(p plan) []string {
	var out []string
	for _, f := range p.def.Fields {
		if f.Kind == value.KindText {
			out = append(out, f.Name)
		}
	}
	return out
}

// refreshRefs re-expands every text field of one row.
func (c *Cache) refreshRefs(ctx context.Context, tx *sql.Tx, p plan, id ids.AnyID, vals map[string]value.Value) error {
	for _, f := range textFields(p) {
		text := ""
		if v, ok := vals[f]; ok && v.Kind() == value.KindText {
			text = v.String()
		}
		if err := c.expandField(ctx, tx, p.def.Name, id.String(), f, text); err != nil {
			return err
		}
	}
	return nil
}

// expandField replaces the row_refs and row_expanded rows of one field.
// A field without references keeps no row_expanded row (readers fall back
// to the column).
func (c *Cache) expandField(ctx context.Context, tx *sql.Tx, table row.Table, id, field, text string) error {
	for _, q := range []string{
		`DELETE FROM "` + tblRefs + `" WHERE entity_table = ? AND entity_id = ? AND field = ?`,
		`DELETE FROM "` + tblExpanded + `" WHERE entity_table = ? AND entity_id = ? AND field = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, table.String(), id, field); err != nil {
			return dbErr("clear references", err)
		}
	}
	if text == "" {
		return nil
	}
	expanded, refs, err := c.exp.Expand(text)
	if err != nil {
		return errs.Wrap(errs.CodeOf(err), "sqlite cache: expand "+table.String()+"."+field+" of "+id, err)
	}
	refs = slices.Compact(slices.Sorted(slices.Values(refs)))
	if len(refs) == 0 {
		return nil
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO "`+tblRefs+`" (entity_table, entity_id, field, ref_id) VALUES (?, ?, ?, ?)`,
			table.String(), id, field, ref); err != nil {
			return dbErr("record reference", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO "`+tblExpanded+`" (entity_table, entity_id, field, text) VALUES (?, ?, ?, ?)`,
		table.String(), id, field, expanded); err != nil {
		return dbErr("record expansion", err)
	}
	return nil
}
