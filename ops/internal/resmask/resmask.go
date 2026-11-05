// Package resmask hides the sensitive values of a resource row for the
// generic row printers (`lore show`, `lore trash show`).
//
// A resource row keeps its typed values in one JSON field, "fields":
// {"fld_…": {"name": "ip", "v": …, …}} (spec 4.4, RES 3.1). The table
// definition of `resources` knows nothing of a kind's own fields, so a
// printer's generic sensitive-field mask (schema.FieldDef.Sensitive)
// cannot see them; Fields reads each kind field row and hides the value
// of every sensitive one (RQ1: hidden in show unless --show-sensitive;
// DELETION 3.11: protections stay in the trash). The "history" field is
// already masked when it is written (ops/settings withHistory).
//
// Layer L5 helper (ops/internal): imports engine, model and schema only.
package resmask

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Field names this package reads.
const (
	// FieldsField is the resources JSON field that holds the values.
	FieldsField     = "fields"
	kindFieldSens   = "sensitive" // schema.TableKindFields bool
	kindFieldExtend = "extends"   // schema.TableKindFields ref to the inherited field
	entryName       = "name"      // a "fields" entry's field name
	entryValue      = "v"         // a "fields" entry's value
	// maxExtends bounds the extends walk (a chain is a few levels: org,
	// store, folder, repo); a longer chain or a cycle is hidden.
	maxExtends = 8
)

// Getter reads one row by reference (wire.App, engine-backed envs).
type Getter interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// Fields returns the "fields" text of resource row r with every sensitive
// entry reduced to its name and mask as its value, and the sorted names
// of the hidden fields. ok is false when r is not a resource row or holds
// no readable "fields" object (nothing to mask). Fail closed: an entry
// whose kind field row cannot be read is hidden.
func Fields(ctx context.Context, env Getter, r row.Row, mask string) (masked string, names []string, ok bool) {
	if r.Table != schema.TableResources {
		return "", nil, false
	}
	v, has := r.Fields[FieldsField]
	if !has || v == nil {
		return "", nil, false
	}
	var raw []byte
	if s, isText := v.(value.Text); isText { // a row decoded with the JSON as text
		raw = []byte(s.String())
	} else {
		b, err := v.MarshalJSON()
		if err != nil {
			return "", nil, false
		}
		raw = b
	}
	var entries map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return "", nil, false
	}
	maskJSON, err := json.Marshal(mask)
	if err != nil {
		return "", nil, false
	}
	for fid, e := range entries {
		if !sensitive(ctx, env, fid) {
			continue
		}
		name := fid
		var n string
		if json.Unmarshal(e[entryName], &n) == nil && n != "" {
			name = n
		}
		entries[fid] = map[string]json.RawMessage{entryName: e[entryName], entryValue: maskJSON}
		names = append(names, name)
	}
	out, err := json.Marshal(entries) // map keys sorted: stable output
	if err != nil {
		return "", nil, false
	}
	slices.Sort(names)
	return string(out), names, true
}

// sensitive reports whether kind field fid, or a field it extends, is
// sensitive; true when a row on the way cannot be read (fail closed).
func sensitive(ctx context.Context, env Getter, fid string) bool {
	for range maxExtends {
		id, err := ids.ParseAny(fid)
		if err != nil {
			return true
		}
		_, f, err := env.Get(ctx, engine.Ref{Table: schema.TableKindFields, ID: id})
		if err != nil || f.Deleted {
			return true
		}
		if b, isBool := f.Fields[kindFieldSens].(value.Bool); isBool && b.Bool() {
			return true
		}
		next := f.Fields[kindFieldExtend] // a ref value, whatever its Go type
		if next == nil || next.Kind() == value.KindNull || next.String() == "" {
			return false
		}
		fid = next.String()
	}
	return true
}
