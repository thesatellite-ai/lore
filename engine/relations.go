package engine

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/direction"
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
//
// It also enforces LORE_TEAM_SPEC F15's direction rule (logic/direction)
// on every one of those references, for every table and every ref or
// list-of-ref field (tag_ids, mission_id, tasklist_id, plan_id,
// superseded_by_id, polymorphic targets, ...): a row written to store from
// may not point at a row only a store of a hidden class holds (client →
// internal or personal, shared → personal), else E_LEAK_BLOCKED. This is
// the one place every schema ref is checked; ops/internal/pointer applies
// the same rule to pointers held outside schema refs (task knowledge_refs).
func (e *Engine) checkRefs(ctx context.Context, from store.Source, def schema.TableDef, fields map[string]value.Value, names []string) error {
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
		holders, err := e.holders(ctx, t)
		if err != nil {
			return err
		}
		if len(holders) == 0 {
			if !t.AllowDangling {
				missing = append(missing, t.Field+" → "+t.ID.String())
			}
			continue
		}
		if err := e.checkDirection(from, t, holders); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		return errs.New(errs.NotFound, "engine: "+string(def.Name)+" refers to rows no attached store holds: "+strings.Join(missing, ", ")).
			WithHint("create the target first, or attach the store that holds it")
	}
	return nil
}

// rosterTables are not pointers into hidden content: a client row may name
// a person or a team (the client copy of an actor is the F18 projection),
// as v0.1.17's checkReferences exempted them.
var rosterTables = map[row.Table]bool{schema.TableActors: true, schema.TableTeams: true}

// checkDirection applies F15 to one reference written to store from whose
// target lives in holders (at least one). The target's id is visible to
// readers of from either way, so the pointer is allowed when any holder's
// class may be named from from's class; otherwise the first refusal is
// returned (fail closed), naming the field and the target.
func (e *Engine) checkDirection(from store.Source, t validate.Target, holders []store.Source) error {
	if rosterTables[e.targetTable(t)] {
		return nil
	}
	fc := e.classOf(from)
	var first error
	for _, h := range holders {
		err := direction.Check(fc, e.classOf(h))
		if err == nil {
			return nil
		}
		if first == nil {
			first = err
		}
	}
	var ce *errs.Error
	if errors.As(first, &ce) {
		named := *ce
		named.Message = "engine: " + t.Field + " → " + t.ID.String() + ": " + ce.Message
		return &named
	}
	return first
}

// classOf places an attached store in its F15 class: a store flagged
// Attached.Client is client (wire flags the repo store in a client repo,
// and when that cannot be checked), `me` and `me/<folder>` personal, any
// other internal.
func (e *Engine) classOf(name store.Source) direction.Class {
	return direction.ClassOf(string(name), e.stores[name].Client)
}

// targetTable is t's table, from its id prefix when the field names none.
func (e *Engine) targetTable(t validate.Target) row.Table {
	if t.Table != "" {
		return t.Table
	}
	if def, ok := e.reg.ByPrefix(t.ID.Prefix()); ok {
		return def.Name
	}
	return ""
}

// holders lists the attached stores (precedence order) where a reference
// target is a live row (and, for a kind ref, a resource of that kind).
func (e *Engine) holders(ctx context.Context, t validate.Target) ([]store.Source, error) {
	table := e.targetTable(t)
	if table == "" {
		return nil, nil
	}
	var out []store.Source
	for _, name := range e.lookupStores(table) { // P3-LOCAL-1
		got, _, err := e.getRow(ctx, name, table, t.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if got.Deleted {
			continue
		}
		if t.Kind.IsZero() {
			out = append(out, name)
			continue
		}
		r, err := got.Decode()
		if err != nil {
			return nil, err
		}
		if k, ok := r.Fields[kindField]; ok && k.String() == t.Kind.String() {
			out = append(out, name)
		}
	}
	return out, nil
}

// dependentRule is one way rows of table point at a row being deleted.
type dependentRule struct {
	table     row.Table
	field     string
	typeField string // polymorphic: must name the deleted row's table
	onDelete  schema.OnDelete
	// list: a many-to-many list-of-ref field (O11). OnDelete then acts on
	// the link only: cascade and set_null remove the deleted id from the
	// list, restrict refuses, tombstone_only leaves it.
	list bool
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
			case schema.ManyToMany:
				if rel.Target == target {
					add(dependentRule{table: def.Name, field: rel.Field, onDelete: rel.OnDelete, list: true})
				}
			case schema.HasMany:
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
	for _, name := range e.allStores() { // P3-LOCAL-1: machine-local rows may point at the row too
		rows, _, err := e.readRows(ctx, name, "")
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
	// any form: a ref, a List of refs or texts, a JSON array (a row file
	// whose array Infer could not type), or text holding an id
	return slices.Contains(value.ItemStrings(v), id.String())
}
