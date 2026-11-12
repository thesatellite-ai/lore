package engine

// emptyref.go — v0.1.17's "" in a ref field (owner report 2026-11-12).
// lore 0.1.17 wrote an empty string for "no ref" (36 tasks with
// tasklist_id "", one assigned_to_actor_id ""). A ref field's value must be
// an id, so every later edit of such a row was refused (E_RESOURCE_INVALID
// "field tasklist_id: want a ref value"), whatever the edit changed, and a
// bulk edit (actor merge) stopped at the first one.
//
//	read (List, Get/locate)  ─▶ an empty-string ref is absent (null): the
//	                            row reads as "no tasklist", as it meant
//	edit (Update: retype)    ─▶ the stored "" becomes null, so the edit
//	                            validates and writes the row without it
//	WithStoredRefs(ctx)      ─▶ reads keep the stored "" (doctor's
//	                            empty_ref check finds them; its --fix edits
//	                            each one, which writes null)
//
// migrate run and store adopt drop them as they convert rows
// (NullEmptyRefs).

import (
	"context"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// storedRefsKey is the context key of WithStoredRefs.
type storedRefsKey struct{}

// WithStoredRefs is ctx whose reads return ref fields as stored, an empty
// string included (doctor's empty_ref check and its repair).
func WithStoredRefs(ctx context.Context) context.Context {
	return context.WithValue(ctx, storedRefsKey{}, true)
}

// storedRefs reports WithStoredRefs.
func storedRefs(ctx context.Context) bool {
	on, _ := ctx.Value(storedRefsKey{}).(bool)
	return on
}

// emptyRef reports whether v is an empty string held by a ref field.
func emptyRef(f schema.FieldDef, v value.Value) bool {
	if f.Kind != value.KindRef || v == nil {
		return false
	}
	t, ok := v.(value.Text)
	return ok && t.String() == ""
}

// EmptyRefFields lists the fields of r that are refs holding an empty
// string (sorted; nil when none or the table is unknown).
func EmptyRefFields(reg *schema.Registry, r row.Row) []string {
	def, ok := reg.Table(r.Table)
	if !ok {
		return nil
	}
	var out []string
	for name, v := range r.Fields {
		if f, ok := def.Field(name); ok && emptyRef(f, v) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// NullEmptyRefs is r without its empty-string ref fields (absent is null)
// and the fields it dropped. r itself is not changed.
func NullEmptyRefs(reg *schema.Registry, r row.Row) (row.Row, []string) {
	names := EmptyRefFields(reg, r)
	if len(names) == 0 {
		return r, nil
	}
	r.Fields = maps.Clone(r.Fields)
	for _, n := range names {
		delete(r.Fields, n)
	}
	return r, names
}

// readRow is r as a read returns it: empty-string refs read as null unless
// ctx asks for the stored values.
func (e *Engine) readRow(ctx context.Context, r row.Row) row.Row {
	if storedRefs(ctx) {
		return r
	}
	out, _ := NullEmptyRefs(e.reg, r)
	return out
}
