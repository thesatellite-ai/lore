// Package canon produces canonical, byte-stable JSON without ever holding a
// value as `any`: input is parsed into a small closed tree and written back
// with sorted keys.
//
// Why it exists: row files are committed to git and compared byte for byte
// by the store contract, so identical data must always give identical bytes.
// The canonical document form is exactly the one v0.1.17's
// saas/pkg/aicoder/canonjson writes (sorted keys, two-space indent, no HTML
// escaping, numbers kept as written, one trailing newline), so a row file
// written by either produces the same bytes. canon_test.go pins that form.
//
// Differences from canonjson, both stricter: a duplicate object key is an
// error (canonjson kept the last one, silently dropping data), and the depth
// limit counts every object and array level.
package canon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

// MaxDepth bounds nesting. Real rows are flat objects with a couple of
// levels inside JSON fields; anything deeper is malformed or hostile (a
// teammate's branch is untrusted input). Same value as canonjson.
const MaxDepth = 32

// indentUnit is the per-level indent of the document form; changing it
// rewrites every committed row file.
const indentUnit = "  "

// Sentinel errors, wrapped with detail by the functions below.
var (
	ErrInvalid      = errors.New("canon: invalid JSON")
	ErrTooDeep      = errors.New("canon: nesting too deep")
	ErrNotObject    = errors.New("canon: top-level value is not an object")
	ErrDuplicateKey = errors.New("canon: duplicate object key")
	ErrTrailing     = errors.New("canon: trailing data after the value")
)

// Kind is the JSON type of a value.
type Kind int

// The closed set of JSON types.
const (
	KindInvalid Kind = iota
	KindNull
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Member is one key of an object with its value as canonical compact JSON.
type Member struct {
	Key string
	Raw []byte
}

// node is the parsed tree: a scalar keeps its canonical literal, an array
// its elements, an object its members in input order (sorted on write).
type node struct {
	kind    Kind
	lit     []byte
	elems   []node
	members []member
}

type member struct {
	key string
	val node
}

// Compact parses one JSON value and returns its canonical compact form.
func Compact(b []byte) ([]byte, error) {
	n, err := parse(b)
	if err != nil {
		return nil, err
	}
	return n.appendCompact(nil), nil
}

// Document returns the canonical document form of a JSON object: sorted
// keys, two-space indent, one trailing newline.
func Document(b []byte) ([]byte, error) {
	n, err := parse(b)
	if err != nil {
		return nil, err
	}
	if n.kind != KindObject {
		return nil, ErrNotObject
	}
	return indent(n.appendCompact(nil))
}

// Object builds the canonical document of an object from members (values in
// any valid JSON form; each is canonicalized). A repeated key is an error.
func Object(members []Member) ([]byte, error) {
	n := node{kind: KindObject, members: make([]member, 0, len(members))}
	for _, m := range members {
		v, err := parse(m.Raw)
		if err != nil {
			return nil, errors.Join(errors.New("canon: key "+quoteKey(m.Key)), err)
		}
		n.members = append(n.members, member{key: m.Key, val: v})
	}
	if err := n.checkDuplicates(); err != nil {
		return nil, err
	}
	return indent(n.appendCompact(nil))
}

// Members parses a JSON object and returns its members sorted by key, each
// value in canonical compact form.
func Members(b []byte) ([]Member, error) {
	n, err := parse(b)
	if err != nil {
		return nil, err
	}
	if n.kind != KindObject {
		return nil, ErrNotObject
	}
	n.sortMembers()
	out := make([]Member, len(n.members))
	for i, m := range n.members {
		out[i] = Member{Key: m.key, Raw: m.val.appendCompact(nil)}
	}
	return out, nil
}

// Elements parses a JSON array and returns each element in canonical compact
// form; any other value is an error.
func Elements(b []byte) ([][]byte, error) {
	n, err := parse(b)
	if err != nil {
		return nil, err
	}
	if n.kind != KindArray {
		return nil, errors.Join(ErrInvalid, errors.New("canon: not an array"))
	}
	out := make([][]byte, len(n.elems))
	for i, e := range n.elems {
		out[i] = e.appendCompact(nil)
	}
	return out, nil
}

// KindOf reports the JSON type of b (KindInvalid when b does not parse).
func KindOf(b []byte) Kind {
	n, err := parse(b)
	if err != nil {
		return KindInvalid
	}
	return n.kind
}

// String returns s encoded as a JSON string exactly as encoding/json writes it
// with HTML escaping off (so `<`, `>` and `&` stay literal).
func String(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding a Go string never fails; the error path is unreachable.
	if err := enc.Encode(s); err != nil {
		return []byte(`""`)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// indent turns canonical compact JSON into the document form.
func indent(compact []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, compact, "", indentUnit); err != nil {
		return nil, errors.Join(ErrInvalid, err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// parse reads exactly one JSON value from b.
func parse(b []byte) (node, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	n, err := parseValue(dec, 0)
	if err != nil {
		return node{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return node{}, ErrTrailing
	}
	return n, nil
}

// parseValue reads the next value. json.Decoder.Token returns json.Token,
// an alias of any: that is the one stdlib boundary of this package, and the
// type switch below narrows it at once into the closed node tree.
func parseValue(dec *json.Decoder, depth int) (node, error) {
	tok, err := dec.Token()
	if err != nil {
		return node{}, errors.Join(ErrInvalid, err)
	}
	switch v := tok.(type) {
	case json.Delim:
		return parseComposite(dec, v, depth+1)
	case string:
		return node{kind: KindString, lit: String(v)}, nil
	case json.Number:
		return node{kind: KindNumber, lit: []byte(v.String())}, nil
	case bool:
		if v {
			return node{kind: KindBool, lit: []byte("true")}, nil
		}
		return node{kind: KindBool, lit: []byte("false")}, nil
	case nil:
		return node{kind: KindNull, lit: []byte("null")}, nil
	}
	return node{}, ErrInvalid
}

// parseComposite reads an object or array after its opening delimiter.
func parseComposite(dec *json.Decoder, open json.Delim, depth int) (node, error) {
	if depth > MaxDepth {
		return node{}, ErrTooDeep
	}
	switch open {
	case '{':
		n := node{kind: KindObject}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return node{}, errors.Join(ErrInvalid, err)
			}
			key, ok := kt.(string)
			if !ok {
				return node{}, ErrInvalid
			}
			v, err := parseValue(dec, depth)
			if err != nil {
				return node{}, err
			}
			n.members = append(n.members, member{key: key, val: v})
		}
		if err := closeDelim(dec, '}'); err != nil {
			return node{}, err
		}
		if err := n.checkDuplicates(); err != nil {
			return node{}, err
		}
		return n, nil
	case '[':
		n := node{kind: KindArray}
		for dec.More() {
			v, err := parseValue(dec, depth)
			if err != nil {
				return node{}, err
			}
			n.elems = append(n.elems, v)
		}
		if err := closeDelim(dec, ']'); err != nil {
			return node{}, err
		}
		return n, nil
	}
	return node{}, ErrInvalid
}

// closeDelim consumes the closing delimiter want.
func closeDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return ErrInvalid
	}
	return nil
}

// checkDuplicates refuses an object with a repeated key.
func (n *node) checkDuplicates() error {
	seen := make(map[string]struct{}, len(n.members))
	for _, m := range n.members {
		if _, dup := seen[m.key]; dup {
			return errors.Join(ErrDuplicateKey, errors.New("canon: key "+quoteKey(m.key)))
		}
		seen[m.key] = struct{}{}
	}
	return nil
}

// sortMembers orders members by key bytes, the order encoding/json uses for
// map keys.
func (n *node) sortMembers() {
	slices.SortFunc(n.members, func(a, b member) int { return strings.Compare(a.key, b.key) })
}

// appendCompact writes n's canonical compact form to dst.
func (n node) appendCompact(dst []byte) []byte {
	switch n.kind {
	case KindObject:
		n.sortMembers()
		dst = append(dst, '{')
		for i, m := range n.members {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, String(m.key)...)
			dst = append(dst, ':')
			dst = m.val.appendCompact(dst)
		}
		return append(dst, '}')
	case KindArray:
		dst = append(dst, '[')
		for i, e := range n.elems {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = e.appendCompact(dst)
		}
		return append(dst, ']')
	default:
		return append(dst, n.lit...)
	}
}

// maxKeyQuote bounds how much of a key an error repeats.
const maxKeyQuote = 64

// quoteKey renders a key for an error message, truncated.
func quoteKey(k string) string {
	if len(k) > maxKeyQuote {
		k = k[:maxKeyQuote]
	}
	return string(String(k))
}
