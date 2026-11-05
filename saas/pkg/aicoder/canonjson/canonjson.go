// Package canonjson is a forwarding shell: the code moved to
// github.com/thesatellite-ai/lore/logic/canonjson (PASS3 P3-PORT-LIVE),
// which holds JSON as the closed canonjson.Value type instead of `any`.
// This shell keeps the v0.1.17 map[string]any API for the old saas
// importers and converts at the edge (ToValue / FromValue); the bytes it
// writes are the root package's, so both paths produce the same files. New
// code imports the root package directly.
package canonjson

import (
	"encoding/json"
	"fmt"

	root "github.com/thesatellite-ai/lore/logic/canonjson"
)

// DefaultMaxDepth forwards to the root package.
const DefaultMaxDepth = root.DefaultMaxDepth

// Errors forwarded from the root package.
var (
	ErrTooDeep   = root.ErrTooDeep
	ErrNotObject = root.ErrNotObject
)

// Encode renders obj in canonical form.
//
// Accepted value types (anything else is an error, never a silent string):
// nil, string, bool, int, int64, float64, json.Number, json.RawMessage,
// map[string]any, []any, and root canonjson Value / Object.
func Encode(obj map[string]any) ([]byte, error) {
	o, err := toObject(obj, 0)
	if err != nil {
		return nil, err
	}
	return root.Encode(o)
}

// Decode parses a JSON object, numbers as json.Number (see the root
// package for the refusals).
func Decode(b []byte, maxDepth int) (map[string]any, error) {
	o, err := root.Decode(b, maxDepth)
	if err != nil {
		return nil, err
	}
	return objectToAny(o), nil
}

// Canonicalize forwards to the root package.
func Canonicalize(b []byte) ([]byte, error) { return root.Canonicalize(b) }

// CompactValue renders any JSON value compactly with sorted keys.
func CompactValue(v any) (string, error) {
	rv, err := ToValue(v)
	if err != nil {
		return "", err
	}
	return root.CompactValue(rv)
}

// ParseValue parses one JSON value of any kind (numbers as json.Number).
func ParseValue(s string) (any, error) {
	v, err := root.ParseValue(s)
	if err != nil {
		return nil, err
	}
	return FromValue(v), nil
}

// ToValue converts a v0.1.17 value tree to a root Value. It refuses any
// type outside the accepted set listed on Encode.
func ToValue(v any) (root.Value, error) { return toValue(v, 0) }

// FromValue converts a root Value to the v0.1.17 tree: nil, bool, string,
// json.Number, []any, map[string]any.
func FromValue(v root.Value) any {
	switch v.Kind() {
	case root.KindBool:
		b, _ := v.AsBool()
		return b
	case root.KindNumber:
		n, _ := v.AsNumber()
		return json.Number(n)
	case root.KindString:
		s, _ := v.AsString()
		return s
	case root.KindArray:
		arr, _ := v.AsArray()
		out := make([]any, len(arr))
		for i, e := range arr {
			out[i] = FromValue(e)
		}
		return out
	case root.KindObject:
		o, _ := v.AsObject()
		return objectToAny(o)
	case root.KindNull:
		return nil
	}
	return nil
}

// ToObject converts a v0.1.17 object tree to a root Object (the value
// types Encode accepts; anything else is an error). Old callers holding a
// live row body as map[string]any convert with it at the edge.
func ToObject(obj map[string]any) (root.Object, error) { return toObject(obj, 0) }

// FromObject converts a root Object to the v0.1.17 tree (see FromValue); a
// nil Object gives a nil map.
func FromObject(o root.Object) map[string]any {
	if o == nil {
		return nil
	}
	return objectToAny(o)
}

func objectToAny(o root.Object) map[string]any {
	out := make(map[string]any, len(o))
	for k, v := range o {
		out[k] = FromValue(v)
	}
	return out
}

func toObject(obj map[string]any, depth int) (root.Object, error) {
	if depth > DefaultMaxDepth {
		return nil, ErrTooDeep
	}
	out := make(root.Object, len(obj))
	for k, v := range obj {
		nv, err := toValue(v, depth+1)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out[k] = nv
	}
	return out, nil
}

func toValue(v any, depth int) (root.Value, error) {
	if depth > DefaultMaxDepth {
		return root.Value{}, ErrTooDeep
	}
	switch x := v.(type) {
	case nil:
		return root.Null(), nil
	case string:
		return root.String(x), nil
	case bool:
		return root.Bool(x), nil
	case json.Number:
		return root.Number(string(x)), nil
	case int:
		return root.Int(int64(x)), nil
	case int64:
		return root.Int(x), nil
	case float64:
		f, ok := root.Float(x)
		if !ok {
			return root.Value{}, fmt.Errorf("canonjson: unsupported float value %v", x)
		}
		return f, nil
	case json.RawMessage:
		return root.Raw(x)
	case root.Value:
		return x, nil
	case root.Object:
		return root.ObjectValue(x), nil
	case map[string]any:
		o, err := toObject(x, depth)
		if err != nil {
			return root.Value{}, err
		}
		return root.ObjectValue(o), nil
	case []any:
		out := make([]root.Value, len(x))
		for i, e := range x {
			ne, err := toValue(e, depth+1)
			if err != nil {
				return root.Value{}, fmt.Errorf("index %d: %w", i, err)
			}
			out[i] = ne
		}
		return root.Array(out...), nil
	default:
		return root.Value{}, fmt.Errorf("canonjson: unsupported value type %T", v)
	}
}
