package schema

import (
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// KindTablePrefix starts the cache table name of every resource kind
// ("server" → "res_server", spec 4.4).
const KindTablePrefix = "res_"

// KindDefFromRows builds the TableDef of a resource kind from its `kinds`
// row and its `kind_fields` rows, the run-time twin of Table(...): the cache,
// the validator and the surfaces treat the result exactly like a built-in
// (SETTINGS ARE DATA: kinds are rows; fixed ops, dynamic kinds at run time).
//
// Every kind table starts with the implicit required text field "name"
// (resources.name). Tombstoned field rows are skipped; field rows of another
// kind are an error (the caller passed the wrong set). The result still
// goes through NewRegistry/With for the cross-table checks.
func KindDefFromRows(kind row.Row, fields []row.Row) (TableDef, error) {
	if kind.Table != TableKinds || kind.Deleted {
		return TableDef{}, kindErr("not a live kinds row")
	}
	kindID, err := ids.As[ids.Kind](kind.ID)
	if err != nil {
		return TableDef{}, err
	}
	name, err := textField(kind, fieldName, true)
	if err != nil {
		return TableDef{}, err
	}
	table, err := row.ParseTable(KindTablePrefix + name)
	if err != nil {
		return TableDef{}, kindErr("kind name " + name + " does not make a table name")
	}
	desc, err := textField(kind, "description", false)
	if err != nil {
		return TableDef{}, err
	}
	def := TableDef{
		Name: table, Prefix: ids.PrefixResource, Description: desc, Synced: false, Kind: kindID,
		Fields: []FieldDef{{Name: fieldName, Kind: value.KindText, Required: true}},
	}
	base, exts := SplitExtensions(fields)
	at := map[string]int{} // base field row id → index in def.Fields
	baseRows := map[string]row.Row{}
	for _, fr := range base {
		f, err := fieldFromRow(kindID, fr)
		if err != nil {
			return TableDef{}, err
		}
		at[fr.ID.String()] = len(def.Fields)
		baseRows[fr.ID.String()] = fr
		def.Fields = append(def.Fields, f)
		if f.Kind == value.KindRef {
			def.Indexes = append(def.Indexes, Index{Fields: []string{f.Name}})
		}
	}
	// RES 3.6: a lower level's extension row only tightens its base field;
	// a conflicting one refuses the whole kind (the caller skips it, other
	// kinds keep loading). An extension whose base is not in this view is
	// ignored (the base level cannot be seen from here).
	for _, er := range exts {
		baseID := ExtendsOf(er)
		i, ok := at[baseID]
		if !ok {
			continue
		}
		if err := CheckExtension(baseRows[baseID], er); err != nil {
			return TableDef{}, err
		}
		f := &def.Fields[i]
		f.Required = f.Required || flagOf(er, "required")
		f.Sensitive = f.Sensitive || flagOf(er, "sensitive")
		f.Internal = f.Internal || flagOf(er, "internal")
	}
	return def, nil
}

// FieldExtends is the kind_fields column naming the inherited field an
// extension row tightens (RES 3.6).
const FieldExtends = "extends"

// ExtendsOf is the base field id an extension row tightens ("" for a
// plain field row).
func ExtendsOf(r row.Row) string {
	v, ok := r.Fields[FieldExtends]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// SplitExtensions splits live kind_fields rows into plain field rows and
// extension rows (tombstones dropped).
func SplitExtensions(rows []row.Row) (base, exts []row.Row) {
	for _, r := range rows {
		switch {
		case r.Deleted:
		case ExtendsOf(r) != "":
			exts = append(exts, r)
		default:
			base = append(base, r)
		}
	}
	return base, exts
}

// CheckExtension reports E_KIND_CONFLICT when extension row ext changes
// what base defines instead of tightening it (RES 3.6 table): a lower level
// may add values, make the field required, mark it sensitive or internal,
// and add pattern/min/max where base has none (min/max may also narrow);
// it may not change the kind, name, type or ref kind, nor replace a
// pattern. An absent or false flag means "inherit", so no row can unmark
// sensitive or internal; that check lives in the commands.
func CheckExtension(base, ext row.Row) error {
	conflict := func(what string) error {
		return errs.New(errs.KindConflict, "field "+textOf(base, fieldName)+" ("+base.ID.String()+") is defined in its own level; extension "+
			ext.ID.String()+" may not change its "+what).
			WithHint("a lower level may add fields, add values, make a field required, or mark it sensitive or internal; rename the field or change the outer definition")
	}
	for _, f := range []string{"kind_id", fieldName, "type", "ref_kind", "pattern"} {
		if e := textOf(ext, f); e != "" && e != textOf(base, f) && (f != "pattern" || textOf(base, f) != "") {
			return conflict(f)
		}
	}
	for _, mm := range []struct {
		f     string
		tight func(e, b float64) bool
	}{{"min", func(e, b float64) bool { return e >= b }}, {"max", func(e, b float64) bool { return e <= b }}} {
		e, b := textOf(ext, mm.f), textOf(base, mm.f)
		if e == "" || b == "" || e == b {
			continue
		}
		ef, eerr := strconv.ParseFloat(e, 64)
		bf, berr := strconv.ParseFloat(b, 64)
		if eerr != nil || berr != nil || !mm.tight(ef, bf) {
			return conflict(mm.f + " (only a narrower bound is allowed)")
		}
	}
	return nil
}

// FoldExtension is base as seen from the extension's level: flags OR-ed,
// values added, pattern/min/max taken from ext where it sets them. The
// result keeps base's id (writes still address the defining row).
func FoldExtension(base, ext row.Row) (row.Row, error) {
	if err := CheckExtension(base, ext); err != nil {
		return row.Row{}, err
	}
	out := base
	out.Fields = make(map[string]value.Value, len(base.Fields))
	for k, v := range base.Fields {
		out.Fields[k] = v
	}
	for _, f := range []string{"required", "sensitive", "internal", "strict"} {
		if flagOf(ext, f) {
			out.Fields[f] = value.BoolOf(true)
		}
	}
	for _, f := range []string{"pattern", "min", "max", "label", "description"} {
		if v, ok := ext.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
			out.Fields[f] = v
		}
	}
	if add := value.ItemStrings(ext.Fields["values"]); len(add) > 0 {
		vals := value.ItemStrings(base.Fields["values"])
		for _, a := range add {
			if !slices.Contains(vals, a) {
				vals = append(vals, a)
			}
		}
		items := make([]value.Value, 0, len(vals))
		for _, s := range vals {
			t, err := value.TextOf(s)
			if err != nil {
				return row.Row{}, err
			}
			items = append(items, t)
		}
		l, err := value.ListOf(value.KindText, items...)
		if err != nil {
			return row.Row{}, err
		}
		out.Fields["values"] = l
	}
	return out, nil
}

// textOf is a field's text form ("" when absent).
func textOf(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// flagOf is a bool field (false when absent or not a bool).
func flagOf(r row.Row, name string) bool {
	b, ok := r.Fields[name].(value.Bool)
	return ok && b.Bool()
}

// fieldFromRow converts one kind_fields row.
func fieldFromRow(kindID ids.ID[ids.Kind], fr row.Row) (FieldDef, error) {
	if fr.Table != TableKindFields {
		return FieldDef{}, kindErr("not a kind_fields row")
	}
	owner, err := textField(fr, "kind_id", true)
	if err != nil {
		return FieldDef{}, err
	}
	if owner != kindID.String() {
		return FieldDef{}, kindErr("field row " + fr.ID.String() + " belongs to another kind")
	}
	var f FieldDef
	if f.Name, err = textField(fr, fieldName, true); err != nil {
		return FieldDef{}, err
	}
	typ, err := textField(fr, "type", true)
	if err != nil {
		return FieldDef{}, err
	}
	if f.Kind, err = value.ParseKind(typ); err != nil {
		return FieldDef{}, err
	}
	if f.Kind == value.KindList {
		f.Elem = value.KindText // kind field rows have no item-kind column yet
	}
	for name, dst := range map[string]*bool{"required": &f.Required, "sensitive": &f.Sensitive, "internal": &f.Internal} {
		if *dst, err = boolField(fr, name); err != nil {
			return FieldDef{}, err
		}
	}
	refKind, err := textField(fr, "ref_kind", false)
	if err != nil {
		return FieldDef{}, err
	}
	if refKind != "" {
		if f.RefKind, err = ids.Parse[ids.Kind](refKind); err != nil {
			return FieldDef{}, err
		}
		f.RefTable = TableResources
	}
	if f.UI.Label, err = textField(fr, "label", false); err != nil {
		return FieldDef{}, err
	}
	w, err := textField(fr, "widget", false)
	if err != nil {
		return FieldDef{}, err
	}
	f.UI.Widget = Widget(w)
	if v, ok := fr.Fields["in_list"]; ok {
		b, isBool := v.(value.Bool)
		if !isBool {
			return FieldDef{}, kindErr("in_list is not a bool")
		}
		f.UI.InList = InListNo
		if b.Bool() {
			f.UI.InList = InListYes
		}
	}
	return f, nil
}

// textField reads a text field of r ("" when absent and not required).
func textField(r row.Row, name string, required bool) (string, error) {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		if required {
			return "", kindErr(r.Table.String() + " row " + r.ID.String() + " has no " + name)
		}
		return "", nil
	}
	t, isText := v.(value.Text)
	if !isText {
		return "", kindErr(r.Table.String() + " row " + r.ID.String() + ": " + name + " is not text")
	}
	return t.String(), nil
}

// boolField reads an optional bool field (absent = false).
func boolField(r row.Row, name string) (bool, error) {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return false, nil
	}
	b, isBool := v.(value.Bool)
	if !isBool {
		return false, kindErr(r.Table.String() + " row " + r.ID.String() + ": " + name + " is not a bool")
	}
	return b.Bool(), nil
}

// kindErr builds a kind-definition error.
func kindErr(msg string) error { return errs.New(errs.InvalidInput, "schema: "+msg) }
