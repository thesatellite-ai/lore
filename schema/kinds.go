package schema

import (
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
// the validator and the surfaces treat the result exactly like a built-in.
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
	for _, fr := range fields {
		if fr.Deleted {
			continue
		}
		f, err := fieldFromRow(kindID, fr)
		if err != nil {
			return TableDef{}, err
		}
		def.Fields = append(def.Fields, f)
		if f.Kind == value.KindRef {
			def.Indexes = append(def.Indexes, Index{Fields: []string{f.Name}})
		}
	}
	return def, nil
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
