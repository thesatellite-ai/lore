// Package canonjson produces canonical, byte-stable JSON for JSON objects
// stored in version control, over a closed JSON value type (Value) instead
// of `any`.
//
// Why it exists: lore commits one JSON file per row (LORE_SYNC_SPEC.md §6).
// If two writes of the same data produced different bytes (reordered keys,
// different indentation, HTML-escaped characters, a number printed two
// ways) every read would show up as a git diff and every merge would
// conflict on noise. Encode guarantees: identical data ⇒ identical bytes,
// on every machine and every run.
//
// Canonical form (byte-identical to the v0.1.17 saas package):
//   - object keys sorted (byte order), recursively
//   - two-space indent, one key per line (git's line merge can then combine
//     edits to different keys of the same object)
//   - no HTML escaping (`<`, `>`, `&` stay literal so bodies stay readable)
//   - numbers kept as written, so integers never become floats
//   - exactly one trailing newline (POSIX text file; no "\ No newline" diffs)
//
// The package is generic: it knows nothing about lore, tables, or git.
// Moved from saas/pkg/aicoder/canonjson (PASS3 P3-PORT-LIVE); the saas
// package is a shell that converts its old map[string]any API to Value.
package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// indentUnit is the per-level indent of canonical output. Changing it would
// rewrite every committed lore file, so it is a format-version decision.
const indentUnit = "  "

// DefaultMaxDepth bounds nesting accepted by Decode and Encode. Real lore
// rows are flat objects with at most a couple of levels inside JSON
// columns; anything deeper is malformed or hostile (a teammate's branch is
// untrusted input).
const DefaultMaxDepth = 32

// ErrTooDeep is returned when input nests deeper than the allowed depth.
var ErrTooDeep = errors.New("canonjson: nesting too deep")

// ErrNotObject is returned when the top-level value is not a JSON object.
var ErrNotObject = errors.New("canonjson: top-level value is not an object")

// Encode renders obj in canonical form (indented, sorted, one trailing
// newline). A nil obj writes as {}.
func Encode(obj Object) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeObject(&buf, obj, 0, true); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// Decode parses a JSON object, keeping numbers as written and refusing
// trailing data, non-object top levels, and nesting deeper than maxDepth
// (pass DefaultMaxDepth unless you have a reason).
func Decode(b []byte, maxDepth int) (Object, error) {
	v, err := parse(b, maxDepth, "decode", "object")
	if err != nil {
		return nil, err
	}
	obj, ok := v.AsObject()
	if !ok {
		return nil, ErrNotObject
	}
	return obj, nil
}

// Canonicalize re-encodes an arbitrary JSON object in canonical form.
func Canonicalize(b []byte) ([]byte, error) {
	obj, err := Decode(b, DefaultMaxDepth)
	if err != nil {
		return nil, err
	}
	return Encode(obj)
}

// CompactValue renders any JSON value (object, array, scalar) compactly with
// sorted keys. Used to turn a canonical embedded value back into the compact
// text a JSON column stores, and as the identity key of a set element.
func CompactValue(v Value) (string, error) {
	var buf bytes.Buffer
	if err := writeValue(&buf, v, 0, false); err != nil {
		return "", fmt.Errorf("canonjson: compact: %w", err)
	}
	return buf.String(), nil
}

// ParseValue parses one JSON value of any kind (numbers kept as written).
func ParseValue(s string) (Value, error) {
	return parse([]byte(s), DefaultMaxDepth, "parse value", "value")
}

// Raw parses a JSON column's stored text into a Value, so a caller can
// embed it in a row and have it canonicalized like any other value (the
// v0.1.17 json.RawMessage case).
func Raw(text []byte) (Value, error) { return ParseValue(string(text)) }

// Equal reports whether a and b are the same JSON value. Numbers compare by
// their literal text (as v0.1.17 compared json.Number), objects by keys.
func Equal(a, b Value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KindNull:
		return true
	case KindBool:
		return a.b == b.b
	case KindNumber, KindString:
		return a.str == b.str
	case KindArray:
		if len(a.arr) != len(b.arr) {
			return false
		}
		for i := range a.arr {
			if !Equal(a.arr[i], b.arr[i]) {
				return false
			}
		}
		return true
	case KindObject:
		if len(a.obj) != len(b.obj) {
			return false
		}
		for k, av := range a.obj {
			bv, ok := b.obj[k]
			if !ok || !Equal(av, bv) {
				return false
			}
		}
		return true
	}
	return false
}

// MarshalJSON writes v compactly with sorted keys, so a Value can sit in a
// struct that encoding/json writes.
func (v Value) MarshalJSON() ([]byte, error) {
	s, err := CompactValue(v)
	return []byte(s), err
}

// UnmarshalJSON reads any JSON value into v, numbers kept as written.
func (v *Value) UnmarshalJSON(b []byte) error {
	nv, err := parse(b, DefaultMaxDepth, "decode", "value")
	if err != nil {
		return err
	}
	*v = nv
	return nil
}

// MarshalJSON writes o compactly with sorted keys (a nil o writes {}).
func (o Object) MarshalJSON() ([]byte, error) { return ObjectValue(o).MarshalJSON() }

// UnmarshalJSON reads a JSON object into o.
func (o *Object) UnmarshalJSON(b []byte) error {
	obj, err := Decode(b, DefaultMaxDepth)
	if err != nil {
		return err
	}
	*o = obj
	return nil
}

// parse checks depth, decodes exactly one JSON value and refuses trailing
// data. op names the error prefix ("decode" / "parse value"), what names
// the thing in the trailing-data error, as the v0.1.17 messages did.
func parse(b []byte, maxDepth int, op, what string) (Value, error) {
	if err := checkDepth(b, maxDepth); err != nil {
		return Value{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return Value{}, fmt.Errorf("canonjson: %s: %w", op, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Value{}, fmt.Errorf("canonjson: trailing data after %s", what)
	}
	return fromRaw(raw)
}

// writeValue appends v at nesting depth, indented when pretty.
func writeValue(buf *bytes.Buffer, v Value, depth int, pretty bool) error {
	if depth > DefaultMaxDepth {
		return ErrTooDeep
	}
	switch v.kind {
	case KindNull:
		buf.WriteString("null")
	case KindBool:
		if v.b {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case KindNumber:
		// encoding/json checks the literal (the v0.1.17 json.Number path),
		// so a bad number fails here exactly as it did then.
		return writeScalar(buf, json.Number(v.str))
	case KindString:
		return writeScalar(buf, v.str)
	case KindArray:
		return writeArray(buf, v.arr, depth, pretty)
	case KindObject:
		return writeObject(buf, v.obj, depth, pretty)
	}
	return nil
}

// writeScalar writes a string or number through encoding/json with HTML
// escaping off, so string escapes (U+2028, invalid UTF-8 …) and number
// checks match the v0.1.17 bytes exactly.
func writeScalar[T string | json.Number](buf *bytes.Buffer, s T) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("canonjson: encode: %w", err)
	}
	buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))
	return nil
}

func writeArray(buf *bytes.Buffer, arr []Value, depth int, pretty bool) error {
	if len(arr) == 0 {
		buf.WriteString("[]")
		return nil
	}
	buf.WriteByte('[')
	for i, e := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		newline(buf, depth+1, pretty)
		if err := writeValue(buf, e, depth+1, pretty); err != nil {
			return fmt.Errorf("index %d: %w", i, err)
		}
	}
	newline(buf, depth, pretty)
	buf.WriteByte(']')
	return nil
}

func writeObject(buf *bytes.Buffer, obj Object, depth int, pretty bool) error {
	if depth > DefaultMaxDepth {
		return ErrTooDeep
	}
	if len(obj) == 0 {
		buf.WriteString("{}")
		return nil
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		newline(buf, depth+1, pretty)
		if err := writeScalar(buf, k); err != nil {
			return err
		}
		buf.WriteByte(':')
		if pretty {
			buf.WriteByte(' ')
		}
		if err := writeValue(buf, obj[k], depth+1, pretty); err != nil {
			return fmt.Errorf("key %q: %w", k, err)
		}
	}
	newline(buf, depth, pretty)
	buf.WriteByte('}')
	return nil
}

// newline starts a new indented line at depth (pretty output only).
func newline(buf *bytes.Buffer, depth int, pretty bool) {
	if !pretty {
		return
	}
	buf.WriteByte('\n')
	for range depth {
		buf.WriteString(indentUnit)
	}
}

// checkDepth walks the token stream and fails once nesting exceeds max. It
// runs before the real decode so a hostile, deeply nested file cannot make
// the decoder recurse arbitrarily.
func checkDepth(b []byte, maxDepth int) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("canonjson: decode: %w", err)
		}
		d, ok := tok.(json.Delim)
		if !ok {
			continue
		}
		switch d {
		case '{', '[':
			depth++
			if depth > maxDepth {
				return ErrTooDeep
			}
		case '}', ']':
			depth--
		}
	}
}
