package tui

// resmask.go — the browser's mask for a resource row's own field values.
//
// A resource row keeps its typed values in one JSON field, "fields":
// {"fld_…": {"name": "pin", "v": …}} (spec 4.4, RES 3.1). The resources
// table definition knows nothing of a kind's fields, so the generic
// sensitive-field mask (schema.FieldDef.Sensitive) never sees them, and
// the browser printed a sensitive value such as a PIN in clear (and its
// /text filter matched on it). The same rule as ops/internal/resmask,
// which `lore show` and `lore trash show` use and which this layer cannot
// import: a kind field marked sensitive, or extending one, is masked; a
// field whose kind field row is missing, deleted or past the extends bound
// is masked too (fail closed). The mask is applied where rows are listed,
// so the rows screen label, the filter and the row screen all see it.

import (
	"encoding/json"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Names this file reads.
const (
	resFieldsField  = "fields"    // resources: the JSON object of values
	kindFieldSens   = "sensitive" // kind_fields: bool
	kindFieldExtend = "extends"   // kind_fields: ref to the inherited field
	resEntryName    = "name"      // a "fields" entry's field name
	resEntryValue   = "v"         // a "fields" entry's value
	// maxExtends bounds the extends walk (org, store, folder, repo: a few
	// levels); a longer chain or a cycle is masked.
	maxExtends = 8
)

// kindFieldInfo is what the mask needs of one kind_fields row.
type kindFieldInfo struct {
	sensitive bool
	extends   string
}

// kindFields reads every live kind_fields row of every store.
func (b *browser) kindFields() (map[string]kindFieldInfo, error) {
	hits, err := b.rows(schema.TableKindFields)
	if err != nil {
		return nil, err
	}
	out := make(map[string]kindFieldInfo, len(hits))
	for _, h := range hits {
		var k kindFieldInfo
		if v, ok := h.r.Fields[kindFieldSens].(value.Bool); ok {
			k.sensitive = v.Bool()
		}
		if v := h.r.Fields[kindFieldExtend]; v != nil && v.Kind() != value.KindNull {
			k.extends = v.String()
		}
		out[h.r.ID.String()] = k
	}
	return out, nil
}

// sensitiveField reports whether kind field fid, or one it extends, is
// sensitive; true when a field on the way is unknown (fail closed).
func sensitiveField(kf map[string]kindFieldInfo, fid string) bool {
	for range maxExtends {
		k, ok := kf[fid]
		if !ok || k.sensitive {
			return true
		}
		if k.extends == "" {
			return false
		}
		fid = k.extends
	}
	return true
}

// maskResource returns r with every sensitive "fields" entry reduced to
// its name and the mask. A "fields" value that is not a readable object
// of entries is replaced by the mask as a whole (nothing of it is shown).
func maskResource(kf map[string]kindFieldInfo, r row.Row) row.Row {
	v, has := r.Fields[resFieldsField]
	if !has || v == nil || v.Kind() == value.KindNull {
		return r
	}
	var raw []byte
	if s, isText := v.(value.Text); isText { // a row decoded with the JSON as text
		raw = []byte(s.String())
	} else if b, err := v.MarshalJSON(); err == nil {
		raw = b
	}
	out := r
	out.Fields = make(map[string]value.Value, len(r.Fields))
	for k, fv := range r.Fields {
		out.Fields[k] = fv
	}
	out.Fields[resFieldsField] = maskedFields(kf, raw)
	return out
}

// maskedFields is the "fields" JSON with sensitive entries masked, or the
// mask alone when raw cannot be read or rewritten.
func maskedFields(kf map[string]kindFieldInfo, raw []byte) value.Value {
	var whole value.Value // nil prints as empty: still nothing of the value
	if t, err := value.TextOf(masked); err == nil {
		whole = t
	}
	var entries map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return whole
	}
	maskJSON, err := json.Marshal(masked)
	if err != nil {
		return whole
	}
	for fid, e := range entries {
		if sensitiveField(kf, fid) {
			entries[fid] = map[string]json.RawMessage{resEntryName: e[resEntryName], resEntryValue: maskJSON}
		}
	}
	b, err := json.Marshal(entries) // map keys sorted: stable output
	if err != nil {
		return whole
	}
	j, err := value.JSONOf(b)
	if err != nil {
		return whole
	}
	return j
}
