package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// Item is one part of a table declaration: a field, a relation, an index or
// a table option. Only this package builds Items.
type Item interface{ apply(t *TableDef) }

// itemFunc adapts a function to Item.
type itemFunc func(t *TableDef)

func (f itemFunc) apply(t *TableDef) { f(t) }

// Table declares a table. It never fails: NewRegistry validates every
// definition at once and reports every problem, so a built-in typo stops
// the binary at startup instead of being half-applied.
func Table(name string, prefix ids.Prefix, items ...Item) TableDef {
	t := TableDef{Name: row.Table(name), Prefix: prefix}
	for _, it := range items {
		it.apply(&t)
	}
	return t
}

// FieldOption refines a field declaration.
type FieldOption func(f *FieldDef)

// Field declares a field of kind k.
func Field(name string, k value.Kind, opts ...FieldOption) Item {
	return itemFunc(func(t *TableDef) {
		f := FieldDef{Name: name, Kind: k}
		for _, o := range opts {
			o(&f)
		}
		t.Fields = append(t.Fields, f)
	})
}

// Text declares a text field.
func Text(name string, opts ...FieldOption) Item { return Field(name, value.KindText, opts...) }

// Number declares a number field.
func Number(name string, opts ...FieldOption) Item { return Field(name, value.KindNumber, opts...) }

// Bool declares a bool field.
func Bool(name string, opts ...FieldOption) Item { return Field(name, value.KindBool, opts...) }

// DateTime declares a datetime field.
func DateTime(name string, opts ...FieldOption) Item {
	return Field(name, value.KindDateTime, opts...)
}

// JSON declares a json field.
func JSON(name string, opts ...FieldOption) Item { return Field(name, value.KindJSON, opts...) }

// List declares a list field whose items are of kind elem.
func List(name string, elem value.Kind, opts ...FieldOption) Item {
	return Field(name, value.KindList, append([]FieldOption{func(f *FieldDef) { f.Elem = elem }}, opts...)...)
}

// Ref declares a ref field pointing at target ("" = any table).
func Ref(name, target string, opts ...FieldOption) Item {
	return Field(name, value.KindRef, append([]FieldOption{func(f *FieldDef) { f.RefTable = row.Table(target) }}, opts...)...)
}

// RefList declares a list field whose items are ids of target rows ("" =
// any table), the field shape a ManyToMany relation names.
func RefList(name, target string, opts ...FieldOption) Item {
	return Field(name, value.KindList, append([]FieldOption{func(f *FieldDef) {
		f.Elem = value.KindRef
		f.RefTable = row.Table(target)
	}}, opts...)...)
}

// Required marks the field as required on every live row.
func Required() FieldOption { return func(f *FieldDef) { f.Required = true } }

// Sensitive hides the field's value from agents and sql output.
func Sensitive() FieldOption { return func(f *FieldDef) { f.Sensitive = true } }

// Internal keeps the field out of client repos.
func Internal() FieldOption { return func(f *FieldDef) { f.Internal = true } }

// MergeBy sets the field's merge strategy (logic/merge).
func MergeBy(s MergeStrategy) FieldOption { return func(f *FieldDef) { f.Merge = s } }

// OneOf closes the field to the given text values.
func OneOf(vals ...string) FieldOption {
	return func(f *FieldDef) { f.Values = append([]string(nil), vals...) }
}

// Describe sets the field's one-line description.
func Describe(s string) FieldOption { return func(f *FieldDef) { f.Description = s } }

// Label overrides the default UI label.
func Label(s string) FieldOption { return func(f *FieldDef) { f.UI.Label = s } }

// WithWidget overrides the default UI widget.
func WithWidget(w Widget) FieldOption { return func(f *FieldDef) { f.UI.Widget = w } }

// ShowInList overrides the default list-view rule.
func ShowInList(show bool) FieldOption {
	return func(f *FieldDef) {
		if show {
			f.UI.InList = InListYes
			return
		}
		f.UI.InList = InListNo
	}
}

// Synced marks the table as stored rows that travel with the store.
func Synced() Item { return itemFunc(func(t *TableDef) { t.Synced = true }) }

// FTS names the full-text indexed fields.
func FTS(fields ...string) Item {
	return itemFunc(func(t *TableDef) { t.FTS = append(t.FTS, fields...) })
}

// FTSWeight sets the bm25 weight of one FTS field (see
// TableDef.FTSWeightOf for the default).
func FTSWeight(field string, w float64) Item {
	return itemFunc(func(t *TableDef) {
		if t.FTSWeights == nil {
			t.FTSWeights = map[string]float64{}
		}
		t.FTSWeights[field] = w
	})
}

// IndexOn adds a non-unique cache index.
func IndexOn(fields ...string) Item {
	return itemFunc(func(t *TableDef) { t.Indexes = append(t.Indexes, Index{Fields: fields}) })
}

// UniqueOn adds a unique cache index.
func UniqueOn(fields ...string) Item {
	return itemFunc(func(t *TableDef) { t.Indexes = append(t.Indexes, Index{Fields: fields, Unique: true}) })
}

// GlobalKey makes the table's natural id the same in every store.
func GlobalKey() Item { return itemFunc(func(t *TableDef) { t.GlobalKey = true }) }

// Description sets the table's one-line description.
func Description(s string) Item { return itemFunc(func(t *TableDef) { t.Description = s }) }

// relation adds r.
func relation(r Relation) Item {
	return itemFunc(func(t *TableDef) { t.Relations = append(t.Relations, r) })
}

// BelongsToRel declares that field holds the id of a target row.
func BelongsToRel(name, field, target string, od OnDelete) Item {
	return relation(Relation{Name: name, Kind: BelongsTo, Field: field, Target: row.Table(target), OnDelete: od})
}

// HasManyRel declares that target's field holds this table's id.
func HasManyRel(name, target, field string) Item {
	return relation(Relation{Name: name, Kind: HasMany, Field: field, Target: row.Table(target), OnDelete: TombstoneOnly})
}

// ManyToManyRel declares that the list-of-ref field holds ids of target
// rows; the cache keeps the cache-only join table join filled from it.
func ManyToManyRel(name, field, target, join string, od OnDelete) Item {
	return relation(Relation{
		Name: name, Kind: ManyToMany, Field: field, Target: row.Table(target),
		JoinTable: row.Table(join), OnDelete: od,
	})
}

// SelfRel declares that field holds the id of another row of this table.
func SelfRel(name, field string, od OnDelete) Item {
	return itemFunc(func(t *TableDef) {
		t.Relations = append(t.Relations, Relation{Name: name, Kind: Self, Field: field, Target: t.Name, OnDelete: od})
	})
}

// PolymorphicRel declares that typeField names the target table, idField holds its id.
func PolymorphicRel(name, typeField, idField string, od OnDelete) Item {
	return relation(Relation{Name: name, Kind: Polymorphic, TypeField: typeField, Field: idField, OnDelete: od})
}
