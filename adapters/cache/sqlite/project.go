package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/resquar"

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
	fieldValues  = "values"  // kind_fields.values: the allowed values
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
	if sr.Table == schema.TableMetaKeys {
		// 3.20: a key turning sensitive (or back) re-indexes its rows
		return c.reindexMetaKey(ctx, tx, st, sr.ID.String())
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
	// ObjectForm: the fields object reads the same built in memory or
	// decoded from the row file.
	if raw, ok := value.ObjectForm(r.Fields[fieldResVals]); ok {
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
	// RES 3.15: a quarantined resource is left out of res_<kind> (project
	// already removed its old copy) and, through indexResource, search.
	if !quarantined(r, info.quar) {
		if err := c.upsertTyped(ctx, tx, st.plans[info.table], source, r, vals); err != nil {
			return err
		}
	}
	// Re-index now that the kind is known (see indexResource). During a
	// table sync the kind table may be refilled before the resources FTS
	// table exists; the resources refill indexes everything then.
	rp, hasRes := st.plans[schema.TableResources]
	if !hasRes || rp.fts == "" {
		return nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, rp.fts).Scan(&n); err != nil {
		return dbErr("find resources fts", err)
	}
	if n == 0 {
		return nil
	}
	return indexResource(ctx, tx, rp, source, r, ftsInfoOf(info))
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
	ftsErr := error(nil)
	if p.def.Name == schema.TableResources {
		info := resFTSInfo{}
		if kind := kindOf(r); !kind.IsZero() {
			if info, ftsErr = ftsInfoFromTables(ctx, tx, kind); ftsErr != nil {
				return ftsErr
			}
		}
		ftsErr = indexResource(ctx, tx, p, source, r, info)
	} else {
		// METADATA 3.17: the row's non-sensitive meta values in f_meta
		ftsVals, metaErr := withMeta(ctx, tx, vals, r)
		if metaErr != nil {
			return metaErr
		}
		ftsErr = upsertFTS(ctx, tx, p, source, r.ID, ftsVals)
	}
	if ftsErr != nil {
		return ftsErr
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
		// ItemStrings: a list of refs (in memory), of text (decoded from
		// the file) or a JSON array all give the same ids.
		for _, item := range value.ItemStrings(vals[j.rel.Field]) {
			to, err := ids.ParseAny(item)
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
		value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef,
		value.KindSecretRef: // the entry name as TEXT (RESOURCES 4); never a secret value
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

// resFTSInfo is what indexing one resource needs from its kind: the kind
// name and, per fld_ id, whether its value may be indexed (known, not
// sensitive, not a secret_ref: a locator is not text to find).
type resFTSInfo struct {
	kindName  string
	indexable map[string]bool
	// quar holds each field's quarantine rule; a quarantined resource is
	// left out of search (RES 3.15).
	quar map[string]resquar.Field
}

// ftsInfoOf reads it from the kind's state (upsertKind: always current,
// built from `_rows` before the batch is projected).
func ftsInfoOf(info kindInfo) resFTSInfo {
	out := resFTSInfo{kindName: strings.TrimPrefix(info.table.String(), schema.KindTablePrefix), indexable: map[string]bool{}, quar: info.quar}
	for id, f := range info.fields {
		out.indexable[id] = !f.Sensitive && f.Kind != value.KindSecretRef
	}
	return out
}

// The fixed statements of ftsInfoFromTables (constant identifiers only).
const (
	kindNameQuery      = `SELECT "` + fieldName + `" FROM "` + schema.TableKinds + `" WHERE id = ? ORDER BY "` + colSource + `" LIMIT 1`
	kindFieldsFTSQuery = `SELECT id, "type", MAX(COALESCE("` + fieldSensitive + `", 0)), MAX(COALESCE("` + fieldName + `", '')), MAX(COALESCE("` + fieldValues + `", '')) FROM "` +
		schema.TableKindFields + `" WHERE "` + fieldKindID + `" = ? GROUP BY id, "type"`
)

// ftsInfoFromTables reads it from the kinds / kind_fields tables (the
// resources upsert and refill, which have no state; both tables sort
// before `resources`, so a refill finds them filled). Rows of a kind in
// the batch being applied are not there yet: upsertKind re-indexes those
// resources when the kind's table is filled. Any copy calling a field
// sensitive hides it (the safe answer when two sources disagree).
func ftsInfoFromTables(ctx context.Context, tx *sql.Tx, kind ids.ID[ids.Kind]) (resFTSInfo, error) {
	out := resFTSInfo{indexable: map[string]bool{}, quar: map[string]resquar.Field{}}
	var name sql.NullString
	err := tx.QueryRowContext(ctx, kindNameQuery, kind.String()).Scan(&name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, dbErr("read kind name", err)
	}
	out.kindName = name.String
	rs, err := tx.QueryContext(ctx, kindFieldsFTSQuery, kind.String())
	if err != nil {
		return out, dbErr("read kind fields", err)
	}
	for rs.Next() {
		var id, typ, fname, vals string
		var sensitive int
		if err := rs.Scan(&id, &typ, &sensitive, &fname, &vals); err != nil {
			return out, errors.Join(dbErr("read kind fields", err), rs.Close())
		}
		out.indexable[id] = sensitive == 0 && typ != string(value.KindSecretRef)
		out.quar[id] = resquar.Field{Name: fname, Type: typ, Values: valuesCell(vals)}
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		return out, dbErr("read kind fields", err)
	}
	return out, nil
}

// indexResource writes resource r's full-text row (RES 3.12, R8): its
// name, its kind's name and every field value info marks indexable. A
// value whose field is unknown is left out (fail closed: it might be
// sensitive).
func indexResource(ctx context.Context, tx *sql.Tx, p plan, source string, r row.Row, info resFTSInfo) error {
	if quarantined(r, info.quar) {
		// RES 3.15: left out of search until fixed.
		if _, err := tx.ExecContext(ctx, p.sql.ftsDeleteID, source, r.ID.String()); err != nil {
			return dbErr("fts delete", err)
		}
		return nil
	}
	vals := map[string]value.Value{}
	if v, ok := r.Fields[fieldName]; ok {
		vals[fieldName] = v
	}
	if t, err := value.TextOf(info.kindName); err == nil && info.kindName != "" {
		vals[ftsResKind] = t
	}
	text, err := resourceValueText(r, info.indexable)
	if err != nil {
		return err
	}
	if t, terr := value.TextOf(text); terr == nil && text != "" {
		vals[ftsResValues] = t
	}
	if vals, err = withMeta(ctx, tx, vals, r); err != nil {
		return err
	}
	return upsertFTS(ctx, tx, p, source, r.ID, vals)
}

// resourceValueText is the searchable text of r's indexable field values,
// separated by spaces (a JSON string as its text, anything else as its
// canonical JSON, whose string and number leaves the tokenizer splits).
func resourceValueText(r row.Row, indexable map[string]bool) (string, error) {
	raw, ok := value.ObjectForm(r.Fields[fieldResVals])
	if !ok {
		return "", nil
	}
	members, err := canon.Members(raw.Raw())
	if err != nil {
		return "", errs.Wrap(errs.DBCorrupt, "sqlite cache: resource "+r.ID.String()+" fields", err)
	}
	var parts []string
	for _, m := range members {
		if !indexable[m.Key] {
			continue
		}
		entry, err := canon.Members(m.Raw)
		if err != nil {
			continue // a broken entry: the validator reports it
		}
		for _, e := range entry {
			if e.Key != resValueKey {
				continue
			}
			var s string
			if json.Unmarshal(e.Raw, &s) == nil {
				parts = append(parts, s)
			} else if string(e.Raw) != "null" {
				parts = append(parts, string(e.Raw))
			}
		}
	}
	return strings.Join(parts, " "), nil
}

// quarantined reports whether any of r's stored values breaks its field's
// rule (RES 3.15, logic/resquar). A field id the kind does not know is not
// a reason (3.1: kept and reported by lint).
func quarantined(r row.Row, quar map[string]resquar.Field) bool {
	if len(quar) == 0 {
		return false
	}
	raw, ok := value.ObjectForm(r.Fields[fieldResVals])
	if !ok {
		return false
	}
	members, err := canon.Members(raw.Raw())
	if err != nil {
		return false
	}
	for _, m := range members {
		f, known := quar[m.Key]
		if !known {
			continue
		}
		entry, err := canon.Members(m.Raw)
		if err != nil {
			continue
		}
		for _, e := range entry {
			if e.Key == resValueKey {
				if _, bad := resquar.Check(f, e.Raw); bad {
					return true
				}
			}
		}
	}
	return false
}

// quarField is a kind_fields row's quarantine rule.
func quarField(fr row.Row) resquar.Field {
	f := resquar.Field{}
	if v, ok := fr.Fields[fieldName]; ok && v != nil {
		f.Name = v.String()
	}
	if v, ok := fr.Fields["type"]; ok && v != nil {
		f.Type = v.String()
	}
	if v, ok := fr.Fields[fieldValues]; ok && v != nil && v.Kind() != value.KindNull {
		f.Values = value.ItemStrings(v)
	}
	return f
}

// valuesCell reads a kind_fields.values cell (a JSON array as TEXT, "" when
// unset) as its items.
func valuesCell(cell string) []string {
	if cell == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(cell), &out) != nil {
		return nil
	}
	return out
}
