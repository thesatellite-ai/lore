package schema

import (
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// Registry is an immutable, validated set of table definitions.
//
// Invariants after NewRegistry: names are unique and valid; every table not
// built from a kind has its own registered prefix; kind tables use the
// resources prefix; every field, index, FTS field and relation refers to
// something that exists.
type Registry struct {
	tables map[row.Table]TableDef
	names  []row.Table
}

// NewRegistry validates defs together and returns every problem found at
// once (errors.Join), so a broken built-in fails at startup with the full
// list.
func NewRegistry(defs ...TableDef) (*Registry, error) {
	r := &Registry{tables: make(map[row.Table]TableDef, len(defs))}
	var problems []error
	prefixes := map[ids.Prefix]row.Table{}
	for _, d := range defs {
		if _, dup := r.tables[d.Name]; dup {
			problems = append(problems, problem(d.Name, "declared twice"))
			continue
		}
		r.tables[d.Name] = d
		r.names = append(r.names, d.Name)
		if !d.Kind.IsZero() {
			if d.Prefix != ids.PrefixResource {
				problems = append(problems, problem(d.Name, "a kind table must use the res prefix"))
			}
			continue
		}
		if other, dup := prefixes[d.Prefix]; dup {
			problems = append(problems, problem(d.Name, "prefix "+string(d.Prefix)+" already used by "+string(other)))
		}
		prefixes[d.Prefix] = d.Name
	}
	slices.Sort(r.names)
	for _, n := range r.names {
		problems = append(problems, r.check(r.tables[n])...)
	}
	if len(problems) > 0 {
		return nil, errs.Wrap(errs.ConfigInvalid, "schema: invalid table definitions", errors.Join(problems...))
	}
	return r, nil
}

// With returns a new registry holding r's tables plus defs (dynamic kinds
// loaded at run time). r itself is unchanged.
func (r *Registry) With(defs ...TableDef) (*Registry, error) {
	all := make([]TableDef, 0, len(r.names)+len(defs))
	for _, n := range r.names {
		all = append(all, r.tables[n])
	}
	return NewRegistry(append(all, defs...)...)
}

// Table returns the definition named name.
func (r *Registry) Table(name row.Table) (TableDef, bool) {
	t, ok := r.tables[name]
	return t, ok
}

// ByPrefix returns the stored (non-kind) table whose ids carry p.
func (r *Registry) ByPrefix(p ids.Prefix) (TableDef, bool) {
	for _, n := range r.names {
		if t := r.tables[n]; t.Prefix == p && t.Kind.IsZero() {
			return t, true
		}
	}
	return TableDef{}, false
}

// Tables returns every definition sorted by name.
func (r *Registry) Tables() []TableDef {
	out := make([]TableDef, len(r.names))
	for i, n := range r.names {
		out[i] = r.tables[n]
	}
	return out
}

// check validates one definition against the registry.
func (r *Registry) check(t TableDef) []error {
	var p []error
	if !t.Name.Valid() {
		p = append(p, problem(t.Name, "not a snake_case table name"))
	}
	if !t.Prefix.Valid() || !t.Prefix.Registered() {
		p = append(p, problem(t.Name, "prefix "+string(t.Prefix)+" is not registered in model/ids"))
	}
	seen := map[string]bool{}
	for _, f := range t.Fields {
		p = append(p, r.checkField(t, f)...)
		if seen[f.Name] {
			p = append(p, problem(t.Name, "field "+f.Name+" declared twice"))
		}
		seen[f.Name] = true
	}
	for _, name := range t.FTS {
		if f, ok := t.Field(name); !ok || f.Kind != value.KindText {
			p = append(p, problem(t.Name, "FTS field "+name+" is not a text field"))
		}
	}
	for _, ix := range t.Indexes {
		if len(ix.Fields) == 0 {
			p = append(p, problem(t.Name, "empty index"))
		}
		for _, name := range ix.Fields {
			if _, ok := t.Field(name); !ok {
				p = append(p, problem(t.Name, "index field "+name+" does not exist"))
			}
		}
	}
	for _, rel := range t.Relations {
		p = append(p, r.checkRelation(t, rel)...)
	}
	return p
}

// checkField validates one field.
func (r *Registry) checkField(t TableDef, f FieldDef) []error {
	var p []error
	if !row.ValidName(f.Name) || row.ReservedName(f.Name) {
		p = append(p, problem(t.Name, "field name "+f.Name+" is invalid or reserved"))
	}
	if _, err := value.ParseKind(string(f.Kind)); err != nil || f.Kind == value.KindNull {
		p = append(p, problem(t.Name, "field "+f.Name+" has no usable kind"))
	}
	if (f.Kind == value.KindList) != (f.Elem != "") {
		p = append(p, problem(t.Name, "field "+f.Name+": list fields, and only they, name an item kind"))
	}
	if f.RefTable != "" {
		if f.Kind != value.KindRef && (f.Kind != value.KindList || f.Elem != value.KindRef) {
			p = append(p, problem(t.Name, "field "+f.Name+" names a ref table but is not a ref or a list of refs"))
		} else if _, ok := r.tables[f.RefTable]; !ok {
			p = append(p, problem(t.Name, "field "+f.Name+" refers to unknown table "+string(f.RefTable)))
		}
	}
	if len(f.Values) > 0 && f.Kind != value.KindText {
		p = append(p, problem(t.Name, "field "+f.Name+": allowed values need a text field"))
	}
	if f.UI.Widget != "" && !f.UI.Widget.Valid() {
		p = append(p, problem(t.Name, "field "+f.Name+": unknown widget "+string(f.UI.Widget)))
	}
	return p
}

// checkRelation validates one relation.
func (r *Registry) checkRelation(t TableDef, rel Relation) []error {
	var p []error
	if !slices.Contains(RelationKinds(), rel.Kind) {
		return append(p, problem(t.Name, "relation "+rel.Name+": unknown kind "+string(rel.Kind)))
	}
	if !slices.Contains(OnDeletes(), rel.OnDelete) {
		p = append(p, problem(t.Name, "relation "+rel.Name+": unknown on-delete "+string(rel.OnDelete)))
	}
	target, hasTarget := r.tables[rel.Target]
	if rel.Kind != Polymorphic && !hasTarget {
		return append(p, problem(t.Name, "relation "+rel.Name+": unknown target "+string(rel.Target)))
	}
	local := t.Field
	switch rel.Kind {
	case BelongsTo, Self:
		if rel.Kind == Self && rel.Target != t.Name {
			p = append(p, problem(t.Name, "relation "+rel.Name+": self relation must target its own table"))
		}
		f, ok := local(rel.Field)
		if !ok || f.Kind != value.KindRef {
			p = append(p, problem(t.Name, "relation "+rel.Name+": field "+rel.Field+" is not a ref field"))
		} else if rel.OnDelete == SetNull && f.Required {
			p = append(p, problem(t.Name, "relation "+rel.Name+": set_null on required field "+rel.Field))
		}
	case HasMany:
		if f, ok := target.Field(rel.Field); !ok || f.Kind != value.KindRef {
			p = append(p, problem(t.Name, "relation "+rel.Name+": "+string(rel.Target)+"."+rel.Field+" is not a ref field"))
		}
	case ManyToMany:
		p = append(p, r.checkManyToMany(t, rel)...)
	case Polymorphic:
		if f, ok := local(rel.TypeField); !ok || f.Kind != value.KindText {
			p = append(p, problem(t.Name, "relation "+rel.Name+": type field "+rel.TypeField+" is not text"))
		}
		if f, ok := local(rel.Field); !ok || f.Kind != value.KindRef {
			p = append(p, problem(t.Name, "relation "+rel.Name+": id field "+rel.Field+" is not a ref field"))
		}
	}
	return p
}

// checkManyToMany validates a many-to-many relation (decision O11): Field
// must be a local list-of-ref field pointing at Target, and JoinTable a
// valid name that no registered table and no other relation uses, because
// the cache creates it as its own table.
func (r *Registry) checkManyToMany(t TableDef, rel Relation) []error {
	var p []error
	at := func(msg string) error { return problem(t.Name, "relation "+rel.Name+": "+msg) }
	f, ok := t.Field(rel.Field)
	switch {
	case !ok:
		p = append(p, at("field "+rel.Field+" does not exist"))
	case f.Kind != value.KindList || f.Elem != value.KindRef:
		p = append(p, at("field "+rel.Field+" is not a list of refs"))
	case f.RefTable != rel.Target:
		p = append(p, at("field "+rel.Field+" refers to "+string(f.RefTable)+", not "+string(rel.Target)))
	}
	if !rel.JoinTable.Valid() {
		return append(p, at("invalid join table name"))
	}
	if _, clash := r.tables[rel.JoinTable]; clash {
		p = append(p, at("join table "+string(rel.JoinTable)+" is also a table"))
	}
	for _, n := range r.names {
		for _, other := range r.tables[n].Relations {
			if other.Kind == ManyToMany && other.JoinTable == rel.JoinTable && (n != t.Name || other.Name != rel.Name) {
				p = append(p, at("join table "+string(rel.JoinTable)+" is also used by "+string(n)+"."+other.Name))
			}
		}
	}
	return p
}

// problem formats one validation problem.
func problem(t row.Table, msg string) error {
	return errors.New(strings.TrimSpace(string(t) + ": " + msg))
}
