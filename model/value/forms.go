package value

import (
	"encoding/json"

	"github.com/thesatellite-ai/lore/model/canon"
)

// A field has two forms: the one the engine builds in memory (a json field
// is JSON, a list of refs is a List of Ref) and the one row.Decode infers
// from the row file (Infer: an object is JSON, an array is a List of the
// items' inferred kind or JSON when mixed, a string is Text, so a ref is
// Text). Code that type-asserts one form misses rows of the other, which is
// how a dependency-cycle check once saw no edges in any stored row. Read
// such a field through the helpers below, which give one answer for both.

// JSONForm returns the JSON of v (MarshalJSON, the same bytes for both
// forms of one field) whatever kind it holds, and false for an absent
// (nil) or null value.
func JSONForm(v Value) ([]byte, bool) {
	if v == nil || v.Kind() == KindNull {
		return nil, false
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, false
	}
	return raw, true
}

// ObjectForm returns v as a JSON object value whatever form it decoded as
// (JSON, or Text holding an object); false when v is not an object.
func ObjectForm(v Value) (JSON, bool) {
	if j, ok := v.(JSON); ok && j.IsObject() {
		return j, true
	}
	if t, ok := v.(Text); ok && canon.KindOf([]byte(t.String())) == canon.KindObject {
		if j, err := JSONOf([]byte(t.String())); err == nil {
			return j, true
		}
	}
	return JSON{}, false
}

// ItemStrings returns the items of a list-like value as strings, whatever
// its form: a List (of Text, Ref or any kind: each item's String), a JSON
// array (string items as their text, other items as their JSON), or a
// single scalar (one item). nil, null and an empty list give none; a JSON
// object gives none (it is not a list).
func ItemStrings(v Value) []string {
	switch x := v.(type) {
	case nil, Null:
		return nil
	case List:
		out := make([]string, 0, len(x.items))
		for _, it := range x.items {
			out = append(out, it.String())
		}
		return out
	case JSON:
		switch canon.KindOf(x.raw) {
		case canon.KindArray:
		case canon.KindObject, canon.KindNull, canon.KindInvalid:
			return nil
		case canon.KindString, canon.KindNumber, canon.KindBool:
			return []string{jsonItem(x.raw)}
		}
		elems, err := canon.Elements(x.raw)
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(elems))
		for _, e := range elems {
			out = append(out, jsonItem(e))
		}
		return out
	}
	return []string{v.String()}
}

// jsonItem is one JSON item as ItemStrings gives it: a string's text, any
// other item's JSON.
func jsonItem(e []byte) string {
	var s string
	if canon.KindOf(e) == canon.KindString && json.Unmarshal(e, &s) == nil {
		return s
	}
	return string(e)
}
