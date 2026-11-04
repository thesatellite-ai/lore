package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// kindField is the resources field naming a resource's kind.
const kindField = "kind"

// checkRefs enforces DOCS O2 at write time: every reference among the
// written fields must point at a live row in one of the attached stores,
// else E_NOT_FOUND (fail closed), unless its relation allows dangling.
// Only the named fields are checked, so an untouched old ref that dangles
// does not block an unrelated edit (lore lint reports it).
func (e *Engine) checkRefs(ctx context.Context, def schema.TableDef, fields map[string]value.Value, names []string) error {
	only := make(map[string]value.Value, len(names))
	for _, n := range names {
		if v, ok := fields[n]; ok {
			only[n] = v
		}
	}
	// a polymorphic type field may be unchanged while its id field changes
	for _, rel := range def.Relations {
		if rel.Kind == schema.Polymorphic {
			if v, ok := fields[rel.TypeField]; ok {
				only[rel.TypeField] = v
			}
		}
	}
	var missing []string
	for _, t := range validate.Targets(def, only) {
		if t.AllowDangling {
			continue
		}
		ok, err := e.exists(ctx, t)
		if err != nil {
			return err
		}
		if !ok {
			missing = append(missing, t.Field+" → "+t.ID.String())
		}
	}
	if len(missing) > 0 {
		return errs.New(errs.NotFound, "engine: "+string(def.Name)+" refers to rows no attached store holds: "+strings.Join(missing, ", ")).
			WithHint("create the target first, or attach the store that holds it")
	}
	return nil
}

// exists reports whether a reference target is a live row in any attached
// store (and, for a kind ref, a resource of that kind).
func (e *Engine) exists(ctx context.Context, t validate.Target) (bool, error) {
	table := t.Table
	if table == "" {
		def, ok := e.reg.ByPrefix(t.ID.Prefix())
		if !ok {
			return false, nil
		}
		table = def.Name
	}
	for _, name := range e.order {
		got, err := e.stores[name].Backend.Get(ctx, table, t.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if got.Deleted {
			continue
		}
		if t.Kind.IsZero() {
			return true, nil
		}
		r, err := got.Decode()
		if err != nil {
			return false, err
		}
		if k, ok := r.Fields[kindField]; ok && k.String() == t.Kind.String() {
			return true, nil
		}
	}
	return false, nil
}

// dependentRule is one way rows of table point at a row being deleted.
type dependentRule struct {
	table     row.Table
	field     string
	typeField string // polymorphic: must name the deleted row's table
	onDelete  schema.OnDelete
}

// dependentRules lists every relation pointing at target: the BelongsTo,
// Self and Polymorphic relations of every table, and target's own HasMany
// relations (their Field lives on the other table). A pair declared both
// ways keeps the BelongsTo side, which owns the field.
func (e *Engine) dependentRules(target row.Table) []dependentRule {
	var out []dependentRule
	seen := map[string]bool{}
	add := func(r dependentRule) {
		k := string(r.table) + "." + r.field
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	for _, def := range e.reg.Tables() {
		for _, rel := range def.Relations {
			switch rel.Kind {
			case schema.BelongsTo, schema.Self:
				if rel.Target == target {
					add(dependentRule{table: def.Name, field: rel.Field, onDelete: rel.OnDelete})
				}
			case schema.Polymorphic:
				add(dependentRule{table: def.Name, field: rel.Field, typeField: rel.TypeField, onDelete: rel.OnDelete})
			case schema.HasMany, schema.ManyToMany:
			}
		}
	}
	if def, ok := e.reg.Table(target); ok {
		for _, rel := range def.Relations {
			if rel.Kind == schema.HasMany {
				add(dependentRule{table: rel.Target, field: rel.Field, onDelete: rel.OnDelete})
			}
		}
	}
	return out
}

// dependent is one live row pointing at a row being deleted.
type dependent struct {
	source store.Source
	row    row.Row
	rule   dependentRule
}

// dependents finds the live rows of every attached store that point at
// (table, id). It reads each store from the beginning (Backend.Read with
// the empty cursor): correct on every backend, O(rows); a cache-backed
// referrer index can replace it once the cache port offers one.
func (e *Engine) dependents(ctx context.Context, table row.Table, id ids.AnyID) ([]dependent, error) {
	rules := e.dependentRules(table)
	if len(rules) == 0 {
		return nil, nil
	}
	byTable := map[row.Table][]dependentRule{}
	for _, r := range rules {
		byTable[r.table] = append(byTable[r.table], r)
	}
	var out []dependent
	for _, name := range e.order {
		rows, _, err := e.stores[name].Backend.Read(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, sr := range rows {
			rs, wanted := byTable[sr.Table]
			if !wanted || sr.Deleted {
				continue
			}
			r, err := sr.Decode()
			if err != nil {
				return nil, err
			}
			for _, rule := range rs {
				if points(r, rule, table, id) {
					out = append(out, dependent{source: name, row: r, rule: rule})
				}
			}
		}
	}
	return out, nil
}

// points reports whether r's rule field holds id (as a ref or a list item).
func points(r row.Row, rule dependentRule, table row.Table, id ids.AnyID) bool {
	if rule.typeField != "" {
		if t, ok := r.Fields[rule.typeField]; !ok || t.String() != string(table) {
			return false
		}
	}
	v, ok := r.Fields[rule.field]
	if !ok {
		return false
	}
	if l, isList := v.(value.List); isList {
		for _, it := range l.Items() {
			if it.String() == id.String() {
				return true
			}
		}
		return false
	}
	return v.String() == id.String() // a ref, or text holding an id when read back without a schema
}
