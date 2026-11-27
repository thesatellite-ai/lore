package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	resVNameKey  = "v_name"  // a closed value's name label beside its val_ id in v (OQ-AV)
	fieldValIDs  = "value_ids"
	// fieldMetaValueKey is meta_values' key column (the key the value belongs to).
	fieldMetaValueKey = "key_id"
	fieldValues       = "values" // kind_fields.values: the allowed values
)

// project brings the typed tables in line with one applied row. fresh
// says no copy of sr's id was in the file before this Apply (keepRaw,
// freshrow.go): then nothing is cleared first. A resource is never
// projected fresh (its kind table, FTS text and referrers have side paths
// of their own).
func (c *Cache) project(ctx context.Context, tx *sql.Tx, st state, source store.Source, sr store.Row, fresh bool) error {
	return c.projectDecoded(ctx, tx, st, source, sr, decodeApplied(sr), fresh)
}

// appliedDecode is one applied row's document decoded once (decodeApplied):
// Apply hands it to project and to the meta quarantine, so a cache build
// from nothing decodes each row file once, not once per step (bug hunt
// H-284, devco:D-1022: the decode memo, cleared by the build's own reads,
// missed and the meta quarantine decoded each row again).
type appliedDecode struct {
	r   row.Row
	err error
}

// errTombstone marks a tombstone's decode: a deleted row is never decoded.
var errTombstone = errs.New(errs.Internal, "sqlite cache: a tombstone is not decoded")

// appliedDecodes counts the documents decodeApplied decoded, so a test can
// tell that Apply decodes each applied row once.
var appliedDecodes atomic.Int64

// decodeApplied decodes sr unless it is a tombstone.
func decodeApplied(sr store.Row) appliedDecode {
	if sr.Deleted {
		return appliedDecode{err: errTombstone}
	}
	appliedDecodes.Add(1)
	r, err := sr.Decode()
	return appliedDecode{r: r, err: err}
}

// projectDecoded is project with sr's document already decoded (d).
func (c *Cache) projectDecoded(ctx context.Context, tx *sql.Tx, st state, source store.Source, sr store.Row, d appliedDecode, fresh bool) error {
	p, known := st.plans[sr.Table]
	isResource := sr.Table == schema.TableResources
	if !known && !isResource {
		return nil // unknown table: kept in _rows only
	}
	fresh = fresh && !isResource && !wasTouched(tx, sr.Table.String(), sr.ID.String())
	if isResource {
		for _, k := range st.kinds {
			if err := c.deleteTyped(ctx, tx, st.plans[k.table], string(source), sr.ID); err != nil {
				return err
			}
		}
	}
	if sr.Deleted {
		if known && !fresh {
			return c.deleteTyped(ctx, tx, p, string(source), sr.ID)
		}
		return nil
	}
	r, err := d.r, d.err
	if err != nil {
		// A document this binary cannot read (a newer _v) stays in _rows
		// only; a newer lore will project it.
		if known && !fresh {
			return c.deleteTyped(ctx, tx, p, string(source), sr.ID)
		}
		return nil
	}
	if known {
		if err := c.upsertTyped(ctx, tx, p, string(source), r, r.Fields, fresh); err != nil {
			return err
		}
	}
	if sr.Table == schema.TableMetaKeys {
		// 3.20: a key turning sensitive (or back) re-indexes its rows
		return c.reindexMetaKey(ctx, tx, st, sr.ID.String())
	}
	if sr.Table == schema.TableMetaValues {
		// MQ14 + R8: an allowed value renamed or relabelled, or applied
		// after the rows naming it, re-indexes the rows of its key
		var key string
		switch k := r.Fields[fieldMetaValueKey].(type) {
		case value.Ref:
			key = k.ID().String()
		case value.Text: // a decoded row file carries the ref as its id text
			key = k.String()
		default: // no key: a broken value row names no rows
		}
		// only a well-formed key id reaches the JSON path of the lookup
		if _, perr := ids.Parse[ids.MetaKey](key); perr == nil {
			return c.reindexMetaKey(ctx, tx, st, key)
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
	// A resource belongs only in the table of the kind it names. The refill
	// of a kind table (state.go) reads every resource row, so without this
	// check a refill (a field added, a migrated or adopted folder) put the
	// whole store into each res_<kind> table (devco:D-794).
	if kind.IsZero() || !ok || kindOf(r) != kind {
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
			v, ok := entryValue(f, m.Raw, info.quar[m.Key])
			if ok {
				vals[f.Name] = v
			}
		}
	}
	// RES 3.15: a quarantined resource is left out of res_<kind> (project
	// already removed its old copy) and, through indexResource, search.
	if !quarantined(r, info.quar) {
		if err := c.upsertTyped(ctx, tx, st.plans[info.table], source, r, vals, false); err != nil {
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
	if err := txQueryRow(ctx, tx, `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, rp.fts).Scan(&n); err != nil {
		return dbErr("find resources fts", err)
	}
	if n == 0 {
		return nil
	}
	return indexResource(ctx, tx, rp, source, r, ftsInfoOf(info))
}

// entryValue reads the "v" of one resources.fields entry as f's kind,
// falling back to the inferred kind.
func entryValue(f schema.FieldDef, entry []byte, q resquar.Field) (value.Value, bool) {
	members, err := canon.Members(entry)
	if err != nil {
		return nil, false
	}
	for _, m := range shownEntry(members, q) {
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
// fresh skips clearing r's old FTS entry, join pairs and references: the
// caller knows there are none (project, freshrow.go). Every other write
// marks the row touched, so a later fresh projection of it in the same
// transaction replaces it instead.
func (c *Cache) upsertTyped(ctx context.Context, tx *sql.Tx, p plan, source string, r row.Row, vals map[string]value.Value, fresh bool) error {
	if !fresh {
		markTouched(tx, p.def.Name.String(), source, r.ID.String())
	}
	args := cellArgs(make([]driver.Value, 0, 2*len(p.columns)), p.columns, source, r, vals)
	if _, err := txExecN(ctx, tx, p.sql.insert, args); err != nil {
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
		if ftsErr == nil {
			// OV34-REFLABEL: resources naming r now index its current name
			ftsErr = reindexReferrers(ctx, tx, p, r.ID)
		}
	} else {
		// METADATA 3.17: the row's non-sensitive meta values in f_meta
		ftsVals, metaErr := withMeta(ctx, tx, source, vals, r)
		if metaErr != nil {
			return metaErr
		}
		ftsErr = upsertFTS(ctx, tx, p, source, r.ID, ftsVals, fresh)
	}
	if ftsErr != nil {
		return ftsErr
	}
	if err := fillJoins(ctx, tx, p, source, r.ID, vals, fresh); err != nil {
		return err
	}
	return c.refreshRefs(ctx, tx, p, r.ID, vals, fresh)
}

// fillJoins replaces the pairs of one row in every join table of p from
// its list-of-ref fields (decision O11). An item that is not a valid id
// (an old or hand-edited row) is skipped: the cache mirrors, the validator
// reports it. fresh: the row has no pairs to clear (upsertTyped).
func fillJoins(ctx context.Context, tx *sql.Tx, p plan, source string, id ids.AnyID, vals map[string]value.Value, fresh bool) error {
	for _, j := range p.joins {
		if !fresh {
			if _, err := txExec(ctx, tx, j.deleteFrom, source, id.String()); err != nil {
				return dbErr("clear pairs of "+j.rel.JoinTable.String(), err)
			}
		}
		// ItemStrings: a list of refs (in memory), of text (decoded from
		// the file) or a JSON array all give the same ids.
		for _, item := range value.ItemStrings(vals[j.rel.Field]) {
			to, err := ids.ParseAny(item)
			if err != nil {
				continue
			}
			if _, err := txExec(ctx, tx, j.insert, source, id.String(), to.String()); err != nil {
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

// bindList returns the VALUES list of a typed insert of n columns. Column
// i takes two anonymous parameters: its value bound as is (TEXT, INTEGER
// or NULL), then a number's JSON literal, read with json_extract so a
// number is stored exactly as SQLite reads a JSON number (INTEGER, or REAL
// by SQLite's own conversion); a column binds one of the two, the other is
// NULL (json_extract of NULL is NULL). Anonymous, not ?NNN: the driver
// matches a numbered parameter to its argument by name, a string per
// parameter and argument pair. Why (bug hunt H-285, devco:D-1023): the
// row travelled as one JSON array read back with json_extract once per
// column, a third of the typed upsert of a cache built from nothing; the
// values stored are the same (TestBoundCellsStoreTheSame).
func bindList(n int) string {
	return strings.TrimSuffix(strings.Repeat("coalesce(?, json_extract(?, '$')), ", n), ", ")
}

// cellArgs appends the two parameters (bindList) of each of cols for r.
func cellArgs(args []driver.Value, cols []column, source string, r row.Row, vals map[string]value.Value) []driver.Value {
	for _, col := range cols {
		v, num := cellFor(col, source, r, vals)
		args = append(args, v, num)
	}
	return args
}

// cellFor is the pair of parameters of one column (bindList): its value,
// or a number's literal.
func cellFor(col column, source string, r row.Row, vals map[string]value.Value) (v, num driver.Value) {
	switch col.name {
	case colID:
		return textArg(r.ID.String()), nil
	case colSource:
		return textArg(source), nil
	case colHLC:
		return textArg(r.HLC.String()), nil
	case row.KeyCreatedAt:
		return timeArg(r.CreatedAt), nil
	case row.KeyUpdatedAt:
		return timeArg(r.UpdatedAt), nil
	case row.KeyCreatedBy:
		return idArg(r.CreatedBy.Any()), nil
	case row.KeyUpdatedBy:
		return idArg(r.UpdatedBy.Any()), nil
	}
	return valueArg(vals[col.name])
}

// timeArg is an audit time (zero = NULL).
func timeArg(t time.Time) driver.Value {
	if t.IsZero() {
		return nil
	}
	return textArg(value.DateTimeFrom(t).String())
}

// idArg is an audit actor (zero = NULL).
func idArg(id ids.AnyID) driver.Value {
	if id.IsZero() {
		return nil
	}
	return textArg(id.String())
}

// valueArg is a field value per the affinity table of the package doc:
// its parameters (cellFor). It switches on the value's own kind, not the
// column's: a value of another kind than declared (an old row) is still
// stored, never dropped.
func valueArg(v value.Value) (val, num driver.Value) {
	if v == nil {
		return nil, nil
	}
	switch v.Kind() {
	case value.KindNull:
		return nil, nil
	case value.KindBool:
		if b, ok := v.(value.Bool); ok && b.Bool() {
			return int64(1), nil
		}
		return int64(0), nil
	case value.KindNumber:
		return nil, v.String()
	case value.KindJSON, value.KindList:
		raw, err := v.MarshalJSON()
		if err != nil {
			return nil, nil
		}
		return textArg(string(raw)), nil
	}
	// text, date, datetime, duration, ip, cidr, dns, url, email, ref and
	// secret_ref (the entry name as TEXT, RESOURCES 4; never a secret value)
	return textArg(v.String()), nil
}

// textArg is s as a TEXT parameter, spelled as the JSON cell it replaces
// read back: each byte that is not valid UTF-8 becomes U+FFFD, as
// encoding/json writes it.
func textArg(s string) driver.Value {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// jsonNull is the JSON null cell (the FTS rows' arrays).
var jsonNull = json.RawMessage("null")

// upsertFTS replaces id's entry in p's FTS table; fresh: id has none yet
// (upsertTyped).
func upsertFTS(ctx context.Context, tx *sql.Tx, p plan, source string, id ids.AnyID, vals map[string]value.Value, fresh bool) error {
	if p.fts == "" {
		return nil
	}
	if !fresh {
		if _, err := txExec(ctx, tx, p.sql.ftsDeleteID, source, id.String()); err != nil {
			return dbErr("fts delete", err)
		}
	}
	cells := []json.RawMessage{canon.String(id.String()), canon.String(source)}
	for _, f := range p.def.FTS {
		cell := jsonNull
		if v, ok := vals[f]; ok && v.Kind() != value.KindNull {
			cell = canon.String(ftsIndexText(v.String())) // ı folded, CJK and emoji as tokens (ftsseg.go)
		}
		cells = append(cells, cell)
	}
	arr, err := json.Marshal(cells)
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite cache: encode fts row", err)
	}
	if fresh {
		return queueFTS(ctx, tx, p, source, id.String(), arr)
	}
	if _, err = txExec(ctx, tx, p.sql.ftsInsert, string(arr)); err != nil {
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
	markTouched(tx, p.def.Name.String(), source, id.String())
	// FTS first: its entry is found through the typed row (addFTS).
	if p.fts != "" {
		if _, err := txExec(ctx, tx, p.sql.ftsDeleteID, source, id.String()); err != nil {
			return dbErr("fts delete", err)
		}
	}
	if _, err := txExec(ctx, tx, p.sql.deleteID, source, id.String()); err != nil {
		return dbErr("delete", err)
	}
	for _, j := range p.joins {
		if _, err := txExec(ctx, tx, j.deleteFrom, source, id.String()); err != nil {
			return dbErr("delete pairs", err)
		}
	}
	for _, q := range []string{
		`DELETE FROM "` + tblRefs + `" WHERE entity_table = ? AND entity_id = ?`,
		`DELETE FROM "` + tblExpanded + `" WHERE entity_table = ? AND entity_id = ?`,
	} {
		if _, err := txExec(ctx, tx, q, p.def.Name.String(), id.String()); err != nil {
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
		if _, err := txExec(ctx, tx, q, p.def.Name.String(), string(source)); err != nil {
			return dbErr("clear references", err)
		}
	}
	if p.fts != "" {
		if _, err := txExec(ctx, tx, p.sql.ftsClearSource, string(source)); err != nil {
			return dbErr("clear fts", err)
		}
	}
	for _, j := range p.joins {
		if _, err := txExec(ctx, tx, j.clearSource, string(source)); err != nil {
			return dbErr("clear pairs", err)
		}
	}
	if _, err := txExec(ctx, tx, p.sql.clearSource, string(source)); err != nil {
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
func (c *Cache) refreshRefs(ctx context.Context, tx *sql.Tx, p plan, id ids.AnyID, vals map[string]value.Value, fresh bool) error {
	for _, f := range textFields(p) {
		text := ""
		if v, ok := vals[f]; ok && v.Kind() == value.KindText {
			text = v.String()
		}
		if err := c.expandField(ctx, tx, p.def.Name, id.String(), f, text, fresh); err != nil {
			return err
		}
	}
	return nil
}

// expandField replaces the row_refs and row_expanded rows of one field.
// A field without references keeps no row_expanded row (readers fall back
// to the column). fresh: the row has no lines to clear (upsertTyped).
func (c *Cache) expandField(ctx context.Context, tx *sql.Tx, table row.Table, id, field, text string, fresh bool) error {
	for _, q := range []string{
		`DELETE FROM "` + tblRefs + `" WHERE entity_table = ? AND entity_id = ? AND field = ?`,
		`DELETE FROM "` + tblExpanded + `" WHERE entity_table = ? AND entity_id = ? AND field = ?`,
	} {
		if fresh {
			break
		}
		if _, err := txExec(ctx, tx, q, table.String(), id, field); err != nil {
			return dbErr("clear references", err)
		}
	}
	if text == "" {
		return nil
	}
	expanded, refs, err := c.exp.Expand(text)
	if err != nil {
		return errs.Prefix("sqlite cache: expand "+table.String()+"."+field+" of "+id, err)
	}
	refs = slices.Compact(slices.Sorted(slices.Values(refs)))
	if len(refs) == 0 {
		return nil
	}
	for _, ref := range refs {
		if _, err := txExec(ctx, tx,
			`INSERT INTO "`+tblRefs+`" (entity_table, entity_id, field, ref_id) VALUES (?, ?, ?, ?)`,
			table.String(), id, field, ref); err != nil {
			return dbErr("record reference", err)
		}
	}
	if _, err := txExec(ctx, tx,
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
	kindFieldsFTSQuery = `SELECT id, "type", MAX(COALESCE("` + fieldSensitive + `", 0)), MAX(COALESCE("` + fieldName + `", '')), MAX(COALESCE("` + fieldValues + `", '')), MAX(COALESCE("` + fieldValIDs + `", '')) FROM "` +
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
	err := txQueryRow(ctx, tx, kindNameQuery, kind.String()).Scan(&name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, dbErr("read kind name", err)
	}
	out.kindName = name.String
	rs, err := txQuery(ctx, tx, kindFieldsFTSQuery, kind.String())
	if err != nil {
		return out, dbErr("read kind fields", err)
	}
	for rs.Next() {
		var id, typ, fname, vals, valIDs string
		var sensitive int
		if err := rs.Scan(&id, &typ, &sensitive, &fname, &vals, &valIDs); err != nil {
			return out, errors.Join(dbErr("read kind fields", err), rs.Close())
		}
		out.indexable[id] = sensitive == 0 && typ != string(value.KindSecretRef)
		out.quar[id] = resquar.Field{Name: fname, Type: typ, Values: valuesCell(vals), ValueIDs: resquar.ValueIDsOf([]byte(valIDs))}
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
		if _, err := txExec(ctx, tx, p.sql.ftsDeleteID, source, r.ID.String()); err != nil {
			return dbErr("fts delete", err)
		}
		return nil
	}
	vals := map[string]value.Value{}
	if v, ok := r.Fields[fieldName]; ok {
		vals[fieldName] = v
	}
	// a resource is found by its aliases as by its name (devco D-817):
	// they are indexed in the name column, at the name's weight
	if as := value.ItemStrings(r.Fields[schema.FieldResourceAliases]); len(as) > 0 {
		named := strings.Join(append(value.ItemStrings(r.Fields[fieldName]), as...), " ")
		if t, err := value.TextOf(strings.TrimSpace(named)); err == nil {
			vals[fieldName] = t
		}
	}
	if t, err := value.TextOf(info.kindName); err == nil && info.kindName != "" {
		vals[ftsResKind] = t
	}
	text, err := resourceValueText(r, info.indexable, info.quar)
	if err != nil {
		return err
	}
	// OV34-REFLABEL (reflabel.go): a ref value is also found by its target's name
	labels, err := refLabels(ctx, tx, source, text)
	if err != nil {
		return err
	}
	if labels != "" {
		text += " " + labels
	}
	if t, terr := value.TextOf(text); terr == nil && text != "" {
		vals[ftsResValues] = t
	}
	if vals, err = withMeta(ctx, tx, source, vals, r); err != nil {
		return err
	}
	return upsertFTS(ctx, tx, p, source, r.ID, vals, false)
}

// resourceValueText is the searchable text of r's indexable field values,
// separated by spaces (a JSON string as its text, anything else as its
// canonical JSON, whose string and number leaves the tokenizer splits).
func resourceValueText(r row.Row, indexable map[string]bool, quar map[string]resquar.Field) (string, error) {
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
		for _, e := range shownEntry(entry, quar[m.Key]) {
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

// shownEntry is a fields entry as readers see it: a closed value's val_
// id(s) in v replaced by the current name(s) from f's value ids, else by
// the v_name label; v_name itself dropped (OQ-AV, FA-RES-OQAV: typed
// column, search and quarantine read names). An entry without a val_ id
// comes back unchanged.
func shownEntry(entry []canon.Member, f resquar.Field) []canon.Member {
	var vName []byte
	for _, m := range entry {
		if m.Key == resVNameKey {
			vName = m.Raw
		}
	}
	if vName == nil && len(f.ValueIDs) == 0 {
		return entry
	}
	out := make([]canon.Member, 0, len(entry))
	for _, m := range entry {
		switch m.Key {
		case resVNameKey:
		case resValueKey:
			out = append(out, canon.Member{Key: resValueKey, Raw: resquar.Shown(f, m.Raw, vName)})
		default:
			out = append(out, m)
		}
	}
	return out
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
		for _, e := range shownEntry(entry, f) {
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
	if v, ok := fr.Fields[fieldValIDs]; ok && v != nil && v.Kind() != value.KindNull {
		if b, err := v.MarshalJSON(); err == nil {
			f.ValueIDs = resquar.ValueIDsOf(b)
		}
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
