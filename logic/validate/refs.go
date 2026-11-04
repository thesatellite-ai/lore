package validate

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Target is one reference a row's fields make: the id a ref field (or an
// item of a list of refs) holds, and where it must point.
//
// Why it exists (DOCS O2): the cache has no foreign keys, so the engine
// checks every Target at write time and lore lint reports dangling ones;
// both read the same list from Targets.
type Target struct {
	Field string
	ID    ids.AnyID
	// Table is the table that must hold ID: the field's RefTable, the table
	// a polymorphic relation's type field names, or "" when the id's prefix
	// decides (a ref to any table, like secret_allows.row_id).
	Table row.Table
	// Kind is set on a kind's ref field: the target must be a resource of
	// that kind (Table is then resources).
	Kind ids.ID[ids.Kind]
	// AllowDangling copies the relation's flag; false (also for a ref field
	// with no relation) means a missing target refuses the write.
	AllowDangling bool
}

// Targets lists every reference in fields (typed values, as Fields
// returns, or the inferred values of a row read back from its file),
// sorted by field then id. Null and absent ref fields make none.
func Targets(def schema.TableDef, fields map[string]value.Value) []Target {
	var out []Target
	for _, f := range def.Fields {
		v, ok := fields[f.Name]
		if !ok || (f.Kind != value.KindRef && (f.Kind != value.KindList || f.Elem != value.KindRef)) {
			continue
		}
		base := Target{Field: f.Name, Table: f.RefTable, Kind: f.RefKind}
		for _, rel := range def.Relations {
			if rel.Field != f.Name {
				continue
			}
			base.AllowDangling = rel.AllowDangling
			if rel.Kind == schema.Polymorphic {
				base.Table = polyTable(fields[rel.TypeField])
			}
		}
		for _, id := range refIDs(v) {
			t := base
			t.ID = id
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Target) int {
		if c := strings.Compare(a.Field, b.Field); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out
}

// Dangling returns the Targets of r whose target does not exist, as exists
// reports it (the engine asks the attached stores; lore lint asks the
// cache). AllowDangling targets are reported too: lint lists them either
// way; the engine refuses only the others.
func Dangling(def schema.TableDef, r row.Row, exists func(Target) bool) []Target {
	if r.Deleted {
		return nil
	}
	var out []Target
	for _, t := range Targets(def, r.Fields) {
		if !exists(t) {
			out = append(out, t)
		}
	}
	return out
}

// refIDs returns the ids a ref value or list of refs holds, in either form:
// typed (Ref, List of Ref) or as decoded from a row file (a ref is Text,
// a list of refs a List of Text). An item that is not an id is skipped:
// field validation reports it.
func refIDs(v value.Value) []ids.AnyID {
	var out []ids.AnyID
	for _, s := range value.ItemStrings(v) {
		if id, err := ids.ParseAny(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// polyTable reads a polymorphic type field; an unusable value leaves the
// table to the id's prefix.
func polyTable(v value.Value) row.Table {
	t, ok := v.(value.Text)
	if !ok {
		return ""
	}
	name, err := row.ParseTable(t.String())
	if err != nil {
		return ""
	}
	return name
}
