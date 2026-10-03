// Package canonjson produces canonical, byte-stable JSON for JSON objects
// stored in version control.
//
// Why it exists: lore commits one JSON file per row (LORE_SYNC_SPEC.md §6).
// If two writes of the same data produced different bytes — reordered keys,
// different indentation, HTML-escaped characters, a float printed two ways —
// every read would show up as a git diff and every merge would conflict on
// noise. Encode guarantees: identical data ⇒ identical bytes, on every
// machine and every run.
//
// Canonical form:
//   - object keys sorted (byte order), recursively, including inside
//     embedded raw JSON values
//   - two-space indent, one key per line (git's line merge can then combine
//     edits to different keys of the same object)
//   - no HTML escaping (`<`, `>`, `&` stay literal so bodies stay readable)
//   - numbers kept as written (json.Number) so integers never become floats
//   - exactly one trailing newline (POSIX text file; no "\ No newline" diffs)
//
// The package is generic: it knows nothing about lore, tables, or git.
package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// indentUnit is the per-level indent of canonical output. Changing it would
// rewrite every committed lore file, so it is a format-version decision.
const indentUnit = "  "

// DefaultMaxDepth bounds nesting accepted by Decode. Real lore rows are flat
// objects with at most a couple of levels inside JSON columns; anything
// deeper is malformed or hostile (a teammate's branch is untrusted input).
const DefaultMaxDepth = 32

// ErrTooDeep is returned when input nests deeper than the allowed depth.
var ErrTooDeep = errors.New("canonjson: nesting too deep")

// ErrNotObject is returned when the top-level value is not a JSON object.
var ErrNotObject = errors.New("canonjson: top-level value is not an object")

// Encode renders obj in canonical form.
//
// Accepted value types (anything else is an error, never a silent string):
// nil, string, bool, int, int64, float64, json.Number, json.RawMessage,
// map[string]any, []any. json.RawMessage values are themselves
// canonicalized, so callers can pass a JSON column's stored text verbatim.
func Encode(obj map[string]any) ([]byte, error) {
	norm, err := normalize(obj, 0)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indentUnit)
	if err := enc.Encode(norm); err != nil {
		return nil, fmt.Errorf("canonjson: encode: %w", err)
	}
	// json.Encoder.Encode already appends exactly one '\n'.
	return buf.Bytes(), nil
}

// Decode parses a JSON object, keeping numbers as json.Number and refusing
// trailing data, non-object top levels, and nesting deeper than maxDepth
// (pass DefaultMaxDepth unless you have a reason).
func Decode(b []byte, maxDepth int) (map[string]any, error) {
	if err := checkDepth(b, maxDepth); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonjson: decode: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("canonjson: trailing data after object")
	}
	obj, ok := v.(map[string]any)
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
// text a JSON column stores.
func CompactValue(v any) (string, error) {
	norm, err := normalizeValue(v, 0)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(norm); err != nil {
		return "", fmt.Errorf("canonjson: compact: %w", err)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// ParseValue parses one JSON value of any kind (numbers as json.Number).
func ParseValue(s string) (any, error) {
	if err := checkDepth([]byte(s), DefaultMaxDepth); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonjson: parse value: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("canonjson: trailing data after value")
	}
	return v, nil
}

// normalize converts an object to the closed set of types encoding/json
// sorts and prints deterministically (map[string]any keys are sorted by the
// encoder).
func normalize(obj map[string]any, depth int) (map[string]any, error) {
	if depth > DefaultMaxDepth {
		return nil, ErrTooDeep
	}
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		nv, err := normalizeValue(v, depth+1)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out[k] = nv
	}
	return out, nil
}

func normalizeValue(v any, depth int) (any, error) {
	if depth > DefaultMaxDepth {
		return nil, ErrTooDeep
	}
	switch x := v.(type) {
	case nil, string, bool, json.Number:
		return x, nil
	case int:
		return json.Number(fmt.Sprint(x)), nil
	case int64:
		return json.Number(fmt.Sprint(x)), nil
	case float64:
		// encoding/json's float formatting is deterministic (shortest
		// round-trip representation), so pass the float through.
		return x, nil
	case json.RawMessage:
		parsed, err := ParseValue(string(x))
		if err != nil {
			return nil, err
		}
		return normalizeValue(parsed, depth)
	case map[string]any:
		return normalize(x, depth)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			ne, err := normalizeValue(e, depth+1)
			if err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			out[i] = ne
		}
		return out, nil
	default:
		return nil, fmt.Errorf("canonjson: unsupported value type %T", v)
	}
}

// checkDepth walks the token stream and fails once nesting exceeds max. It
// runs before the real decode so a hostile, deeply nested file cannot make
// the decoder recurse arbitrarily.
func checkDepth(b []byte, max int) error {
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
			if depth > max {
				return ErrTooDeep
			}
		case '}', ']':
			depth--
		}
	}
}
