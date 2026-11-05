package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// errEmptyValue is returned for an empty JSON text where a value belongs.
var errEmptyValue = errors.New("canonjson: empty value")

// Kind is the JSON type of a Value.
type Kind int

// The closed set of JSON types. The zero Kind is KindNull, so the zero
// Value is JSON null (the v0.1.17 package's nil).
const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value is one JSON value held without `any`: a closed tree whose scalars
// keep their exact text (a number is its literal as written, never a
// float64, so 9007199254740993 survives a round trip).
//
// Invariant: only the field matching kind is meaningful; constructors are
// the only way to build a non-null Value, so a Value never carries two
// payloads.
type Value struct {
	kind Kind
	// str is the string value (KindString) or the number literal
	// (KindNumber).
	str string
	b   bool
	arr []Value
	obj Object
}

// Object is a JSON object: keys in no order (they are sorted on write).
type Object map[string]Value

// Null returns JSON null.
func Null() Value { return Value{} }

// Bool returns a JSON boolean.
func Bool(b bool) Value { return Value{kind: KindBool, b: b} }

// String returns a JSON string.
func String(s string) Value { return Value{kind: KindString, str: s} }

// Int returns a JSON number for an integer.
func Int(n int64) Value { return Value{kind: KindNumber, str: strconv.FormatInt(n, 10)} }

// Float returns a JSON number for f, written the way encoding/json writes a
// float64 (the shortest text that reads back as f), so a v0.1.17 float
// column keeps its bytes. NaN and ±Inf have no JSON form: ok is false.
func Float(f float64) (v Value, ok bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Value{}, false
	}
	b, err := json.Marshal(f)
	if err != nil {
		return Value{}, false
	}
	return Number(string(b)), true
}

// Number returns a JSON number with the literal written as given. The
// literal is checked when the value is written (Encode / Compact), the
// moment encoding/json checked a json.Number in v0.1.17, so the error
// surfaces at the same call.
func Number(lit string) Value { return Value{kind: KindNumber, str: lit} }

// Array returns a JSON array of the given elements (none gives []).
func Array(elems ...Value) Value { return Value{kind: KindArray, arr: elems} }

// ObjectValue returns o as a JSON value (a nil o writes as {}).
func ObjectValue(o Object) Value { return Value{kind: KindObject, obj: o} }

// Kind reports the value's JSON type.
func (v Value) Kind() Kind { return v.kind }

// IsNull reports JSON null.
func (v Value) IsNull() bool { return v.kind == KindNull }

// AsString returns the string and true for a JSON string.
func (v Value) AsString() (string, bool) { return v.str, v.kind == KindString }

// AsBool returns the boolean and true for a JSON boolean.
func (v Value) AsBool() (bool, bool) { return v.b, v.kind == KindBool }

// AsNumber returns the number literal and true for a JSON number.
func (v Value) AsNumber() (string, bool) { return v.str, v.kind == KindNumber }

// AsArray returns the elements and true for a JSON array. The slice is the
// value's own; callers that change it must copy first.
func (v Value) AsArray() ([]Value, bool) { return v.arr, v.kind == KindArray }

// AsObject returns the object and true for a JSON object. The map is the
// value's own; callers that change it must clone first.
func (v Value) AsObject() (Object, bool) { return v.obj, v.kind == KindObject }

// Str returns the string under key, "" when the key is missing or not a
// string (the `s, _ := doc[k].(string)` idiom of the old map form).
func (o Object) Str(key string) string {
	s, _ := o[key].AsString()
	return s
}

// Has reports whether key is present (null counts as present).
func (o Object) Has(key string) bool {
	_, ok := o[key]
	return ok
}

// Clone returns a shallow copy of o (values are shared; a Value is never
// changed in place by this package). A nil o clones to nil.
func (o Object) Clone() Object {
	if o == nil {
		return nil
	}
	out := make(Object, len(o))
	for k, v := range o {
		out[k] = v
	}
	return out
}

// fromRaw turns one JSON value's text into a Value, numbers kept as written.
// raw has already passed the scanner (it came out of a json.Decoder), so
// only object and array members are decoded again, one level at a time.
func fromRaw(raw json.RawMessage) (Value, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return Value{}, errEmptyValue
	}
	switch t[0] {
	case '{':
		var m map[string]json.RawMessage
		if err := json.Unmarshal(t, &m); err != nil {
			return Value{}, err
		}
		o := make(Object, len(m))
		for k, r := range m {
			v, err := fromRaw(r)
			if err != nil {
				return Value{}, err
			}
			o[k] = v
		}
		return ObjectValue(o), nil
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(t, &list); err != nil {
			return Value{}, err
		}
		out := make([]Value, len(list))
		for i, r := range list {
			v, err := fromRaw(r)
			if err != nil {
				return Value{}, err
			}
			out[i] = v
		}
		return Array(out...), nil
	case '"':
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return Value{}, err
		}
		return String(s), nil
	case 't':
		return Bool(true), nil
	case 'f':
		return Bool(false), nil
	case 'n':
		return Null(), nil
	default:
		return Number(string(t)), nil
	}
}
