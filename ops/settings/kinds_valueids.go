package settings

// Allowed values with permanent ids and labels (OQ-AV, as built in 0.1.18
// by FA-RES-OQAV). Flow:
//
//	field add --values a,b        ─► value_ids [{id: val_…, name: a}, {id, name: b}]
//	field edit --add-value c      ─► syncValueIDs: new val_ id for c
//	           --rename-value a=x ─► the entry keeps its id, name becomes x
//	           --remove-value b   ─► the entry goes
//	           --value-label a=A  ─► the entry's label
//	field edit --to <nearer place> ─► the extension row's own values get ids
//	effectiveFields               ─► an extension's entries join the base's
//	resource add/edit --set f=A   ─► normaliseValue: label or val_ id ─► name
//
// FA-RES-OQAV: the field row keeps `values` (the names every reader, the
// cache and the extension fold use) and gains `value_ids`; resource rows
// keep storing the value name in 0.1.18 (a rename still rewrites them,
// OV61-RENAMEVALUE); storing the val_ id in rows is the remaining OQ-AV step.

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// fValueIDs is the kind_fields column holding the value entries.
const fValueIDs = "value_ids"

// ValueEntry is one allowed value: its permanent id, its snake_case name
// and an optional display label.
type ValueEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// valueEntries reads a field row's value_ids; a missing or unreadable
// column reads as none (a field made before value ids gets them on its
// next values edit).
func valueEntries(r row.Row) []ValueEntry {
	v, ok := r.Fields[fValueIDs]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return nil
	}
	// A decoded row may type the array as a list of JSON objects rather
	// than one JSON value; its JSON form is the same either way.
	b, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	var out []ValueEntry
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// valueEntriesValue encodes entries for the column.
func valueEntriesValue(es []ValueEntry) (value.Value, error) {
	b, err := json.Marshal(es)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "kind field: encode value ids", err)
	}
	return value.JSONOf(b)
}

// syncValueIDs returns the entries for the allowed values names: an entry
// of prev (after renames) keeps its id and label, a new name gets a new
// val_ id, a name no longer allowed loses its entry; labels (name=text)
// are applied last and must name an allowed value.
func syncValueIDs(field string, prev []ValueEntry, names []string, renames []valueRename, labels []string) ([]ValueEntry, error) {
	byName := make(map[string]ValueEntry, len(prev))
	for _, e := range prev {
		for _, r := range renames {
			if e.Name == r.old {
				e.Name = r.new
			}
		}
		byName[e.Name] = e
	}
	out := make([]ValueEntry, 0, len(names))
	for _, n := range names {
		e, ok := byName[n]
		if !ok {
			id, err := ids.New[ids.MetaValue]()
			if err != nil {
				return nil, errs.Wrap(errs.Internal, "kind field: new value id", err)
			}
			e = ValueEntry{ID: id.String(), Name: n}
		}
		out = append(out, e)
	}
	for _, pair := range labels {
		n, label, ok := strings.Cut(pair, "=")
		if !ok || n == "" {
			return nil, errs.New(errs.InvalidInput, "--value-label takes name=label, not "+quote(pair))
		}
		i := slices.IndexFunc(out, func(e ValueEntry) bool { return e.Name == n })
		if i < 0 {
			return nil, errs.New(errs.InvalidInput, field+" has no allowed value "+quote(n)).WithHint("allowed: " + strings.Join(names, ", "))
		}
		out[i].Label = strings.TrimSpace(label)
	}
	return out, nil
}

// foldValueIDs adds an extension row's entries to the base row's (an
// extension adds values with their own ids, RES 3.6); r is the folded row.
func foldValueIDs(r row.Row, ext row.Row) (row.Row, error) {
	add := valueEntries(ext)
	if len(add) == 0 {
		return r, nil
	}
	es := valueEntries(r)
	for _, e := range add {
		if !slices.ContainsFunc(es, func(x ValueEntry) bool { return x.Name == e.Name }) {
			es = append(es, e)
		}
	}
	v, err := valueEntriesValue(es)
	if err != nil {
		return row.Row{}, err
	}
	r.Fields = maps.Clone(r.Fields) // never write into the caller's row
	r.Fields[fValueIDs] = v
	return r, nil
}

// normaliseValue maps an input naming an allowed value by its label
// (case-insensitive) or its val_ id to the value's name; any other input
// comes back unchanged (the allowed-values check then judges it).
func normaliseValue(def row.Row, raw string) string {
	if slices.Contains(texts(def, fValues), raw) {
		return raw
	}
	for _, e := range valueEntries(def) {
		if e.ID == raw || (e.Label != "" && strings.EqualFold(e.Label, raw)) {
			return e.Name
		}
	}
	return raw
}

// valueWords are a field's allowed values as kind show prints them:
// "name (Label)" when the value has a label.
func valueWords(f Field) []string {
	out := slices.Clone(f.Values)
	for i, n := range out {
		for _, e := range f.ValueIDs {
			if e.Name == n && e.Label != "" {
				out[i] = n + " (" + e.Label + ")"
			}
		}
	}
	return out
}

// Kind caps (RES 3.1, A-RES-6, FA-RES-5): fixed for every actor, not
// policy keys; over one is E_KIND_LIMIT.
const (
	maxKindsPerPlace = 200
	maxFieldsPerKind = 150 // counted on the effective kind (levels combined)
	maxValuesField   = 200
)

// kindLimit is the E_KIND_LIMIT refusal.
func kindLimit(what string, limit int) error {
	return errs.New(errs.KindLimit, what+": over the cap of "+strconv.Itoa(limit)+"; nothing written").
		WithHint("split the kind, or remove fields or values")
}

// checkValuesCap refuses more than maxValuesField allowed values.
func checkValuesCap(field string, vals []string) error {
	if len(vals) > maxValuesField {
		return kindLimit(field+" has "+strconv.Itoa(len(vals))+" allowed values", maxValuesField)
	}
	return nil
}

// valueIDsOf maps a closed value given by name (a JSON string, or a list
// of them) to its val_ id(s) by field row f's value entries; false when f
// has no entries or a name has none (stored as the name, the 0.1.17-era
// and pre-id shape, read the same way).
func valueIDsOf(f row.Row, names json.RawMessage) (json.RawMessage, bool) {
	es := valueEntries(f)
	if len(es) == 0 {
		return nil, false
	}
	idOf := func(n string) (string, bool) {
		for _, e := range es {
			if e.Name == n {
				return e.ID, true
			}
		}
		return "", false
	}
	var one string
	if json.Unmarshal(names, &one) == nil {
		id, ok := idOf(one)
		if !ok {
			return nil, false
		}
		b, err := json.Marshal(id)
		return b, err == nil
	}
	var many []string
	if json.Unmarshal(names, &many) != nil || len(many) == 0 {
		return nil, false
	}
	out := make([]string, len(many))
	for i, n := range many {
		id, ok := idOf(n)
		if !ok {
			return nil, false
		}
		out[i] = id
	}
	b, err := json.Marshal(out)
	return b, err == nil
}

// decode is decodeValues with each closed value's name refreshed from d's
// field rows by its val_ id (OQ-AV: a renamed value changes the field row
// only; the row's v_name is a label that may be stale).
func (d kindDef) decode(r located) (resValues, error) {
	out, err := decodeValues(r)
	if err != nil || len(out.valIDs) == 0 {
		return out, err
	}
	for fid, raw := range out.valIDs {
		f, ok := d.byID[fid]
		if !ok {
			continue
		}
		if names, ok := namesOf(f.Row, raw); ok {
			out.vals[fid] = names
		}
	}
	return out, nil
}

// namesOf is valueIDsOf's inverse: the current name(s) of stored val_ id(s).
func namesOf(f row.Row, ids json.RawMessage) (json.RawMessage, bool) {
	es := valueEntries(f)
	nameOf := func(id string) (string, bool) {
		for _, e := range es {
			if e.ID == id {
				return e.Name, true
			}
		}
		return "", false
	}
	var one string
	if json.Unmarshal(ids, &one) == nil {
		n, ok := nameOf(one)
		if !ok {
			return nil, false
		}
		b, err := json.Marshal(n)
		return b, err == nil
	}
	var many []string
	if json.Unmarshal(ids, &many) != nil {
		return nil, false
	}
	out := make([]string, len(many))
	for i, id := range many {
		n, ok := nameOf(id)
		if !ok {
			return nil, false
		}
		out[i] = n
	}
	b, err := json.Marshal(out)
	return b, err == nil
}

// valueAliases are every label and val_ id of f's allowed values: --where
// accepts them beside the names (OQ-AV).
func valueAliases(f row.Row) []string {
	var out []string
	for _, e := range valueEntries(f) {
		out = append(out, e.ID)
		if e.Label != "" {
			out = append(out, e.Label)
		}
	}
	return out
}

// withAliases adds each name's label and val_ id to names, so a --where
// term naming a value by any of them matches.
func withAliases(f row.Row, names []string) []string {
	out := slices.Clone(names)
	for _, e := range valueEntries(f) {
		if slices.Contains(names, e.Name) {
			out = append(out, e.ID)
			if e.Label != "" {
				out = append(out, e.Label)
			}
		}
	}
	return out
}
