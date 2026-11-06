// Package yamlerr decodes YAML files people write by hand (config.yaml,
// .lore/stores.yaml, task templates, mission plans, …) and turns yaml.v3's
// decode errors into text a person can act on: every problem names its line
// and the key it sits under ("line 3: unknown key stores[0].urll", "line 2:
// lease.ttl: expected a whole number, got text `soon`"), and never a Go type
// ("not found in type attach.Entry", "into orgconf.Live" are lore's
// internals, not the user's file).
//
// The decode target's type is known to Decode (a type parameter), so a
// named Go type in yaml's message is mapped back to the kind of value it
// wants (a list, a mapping, text, …) instead of being guessed from its name.
package yamlerr

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Mode is how strictly Decode treats keys the target does not have.
type Mode int

const (
	// Lenient ignores unknown keys (yaml.Unmarshal's behaviour): a file
	// lore only partly reads, such as config.yaml read for one block.
	Lenient Mode = iota
	// Strict refuses unknown keys (yaml's KnownFields): a file whose every
	// key lore owns, such as .lore/stores.yaml or a task template.
	Strict
)

// Error is a decode failure: Problems are the user-facing lines; Unwrap
// gives yaml.v3's own error (a *yaml.TypeError stays reachable through
// errors.As for callers that tell a bad value from a syntax error).
type Error struct {
	Problems []string
	cause    error
}

// Error joins the problems with "; ".
func (e *Error) Error() string { return strings.Join(e.Problems, "; ") }

// Unwrap returns yaml.v3's error.
func (e *Error) Unwrap() error { return e.cause }

// Decode decodes the first YAML document of raw into dst. Empty input is
// no error and leaves dst as it was (yaml.Unmarshal's behaviour, also under
// Strict). A failure is an *Error.
func Decode[T any](raw []byte, dst *T, mode Mode) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(mode == Strict)
	err := dec.Decode(dst)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return &Error{Problems: problems(raw, err, kindsOf(reflect.TypeFor[T]())), cause: err}
}

// Problems returns the user-facing lines of err, a yaml.v3 error from
// decoding raw into a T (for a caller that decoded by other means). A non-
// yaml error comes back as its own text.
func Problems[T any](raw []byte, err error) []string {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Problems
	}
	return problems(raw, err, kindsOf(reflect.TypeFor[T]()))
}

// yaml.v3's message shapes (decode.go): an unknown key under KnownFields, a
// value of the wrong kind, and the syntax errors' prefix.
var (
	unknownRe   = regexp.MustCompile(`^line (\d+): field (.+?) not found in type .+$`)
	mismatchRe  = regexp.MustCompile("^line (\\d+): cannot unmarshal (!!\\w+)(?: `(.*)`)? into (.+)$")
	syntaxStart = "yaml: "
)

// problems maps every message of err to its user line.
func problems(raw []byte, err error, kinds map[string]reflect.Kind) []string {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return []string{strings.TrimPrefix(err.Error(), syntaxStart)}
	}
	root := parseNode(raw)
	out := make([]string, 0, len(te.Errors))
	for _, m := range te.Errors {
		out = append(out, problem(root, m, kinds))
	}
	return out
}

// problem is one TypeError message in the user's words.
func problem(root *yaml.Node, m string, kinds map[string]reflect.Kind) string {
	if g := unknownRe.FindStringSubmatch(m); g != nil {
		line, _ := strconv.Atoi(g[1]) // \d+ always parses
		return "line " + g[1] + ": unknown key " + keyPath(root, line, g[2])
	}
	if g := mismatchRe.FindStringSubmatch(m); g != nil {
		line, _ := strconv.Atoi(g[1]) // \d+ always parses
		got := gotText(g[2], g[3])
		where := valuePath(root, line, g[2])
		if where == "" {
			where = "the file"
		}
		return "line " + g[1] + ": " + where + ": expected " + wantText(g[4], kinds) + ", got " + got
	}
	return m // an UnmarshalYAML method's own message (lore's own words)
}

// parseNode is raw's document node, nil when it does not parse.
func parseNode(raw []byte) *yaml.Node {
	var n yaml.Node
	if yaml.Unmarshal(raw, &n) != nil || len(n.Content) == 0 {
		return nil
	}
	return n.Content[0]
}

// keyPath is the dotted path of the mapping key `key` written on line, or
// key alone when the file cannot be walked.
func keyPath(root *yaml.Node, line int, key string) string {
	p, ok := "", false
	walk(root, "", func(n *yaml.Node, path string, isKey bool) bool {
		if isKey && n.Line == line && n.Value == key {
			p, ok = path, true
			return true
		}
		return false
	})
	if !ok {
		return key
	}
	return p
}

// valuePath is the path of the first value node (document order) with tag
// on line; "" for the document itself or when none is found.
func valuePath(root *yaml.Node, line int, tag string) string {
	p := ""
	walk(root, "", func(n *yaml.Node, path string, isKey bool) bool {
		if !isKey && n.Line == line && n.ShortTag() == tag {
			p = path
			return true
		}
		return false
	})
	return p
}

// walk visits n and its descendants in document order with their paths
// (a.b, list[0].c); a mapping key is visited with the path it opens. visit
// returns true to stop.
func walk(n *yaml.Node, path string, visit func(n *yaml.Node, path string, isKey bool) bool) bool {
	if n == nil {
		return false
	}
	if visit(n, path, false) {
		return true
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			child := k.Value
			if path != "" {
				child = path + "." + k.Value
			}
			if visit(k, child, true) || walk(v, child, visit) {
				return true
			}
		}
	case yaml.SequenceNode:
		for i, v := range n.Content {
			if walk(v, path+"["+strconv.Itoa(i)+"]", visit) {
				return true
			}
		}
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
	}
	return false
}

// gotText names what the file holds: yaml's tag, with a scalar's text.
func gotText(tag, value string) string {
	names := map[string]string{
		"!!str": "text", "!!int": "a whole number", "!!float": "a number", "!!bool": "true or false",
		"!!seq": "a list", "!!map": "a mapping", "!!null": "nothing", "!!timestamp": "a date", "!!binary": "binary data",
	}
	name, ok := names[tag]
	if !ok {
		name = "a " + strings.TrimPrefix(tag, "!!") + " value"
	}
	if value != "" {
		return name + " `" + value + "`"
	}
	return name
}

// durationType is yaml.v3's special case: a time.Duration is written as
// text such as 30s, never as a number of nanoseconds.
var durationType = reflect.TypeFor[time.Duration]()

// wantText names what the target wants, from the Go type yaml printed.
func wantText(goType string, kinds map[string]reflect.Kind) string {
	if goType == durationType.String() {
		return "a duration such as 30s"
	}
	k, ok := kinds[goType]
	if !ok {
		switch {
		case strings.HasPrefix(goType, "[]"):
			k = reflect.Slice
		case strings.HasPrefix(goType, "map["), strings.HasPrefix(goType, "struct {"):
			k = reflect.Map
		default:
			return "a different kind of value"
		}
	}
	switch k {
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "a mapping (key: value lines)"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Bool:
		return "true or false"
	case reflect.String:
		return "text"
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.Interface, reflect.Pointer, reflect.UnsafePointer:
	}
	return "a different kind of value"
}

// kindsOf maps every type reachable from t (fields, elements, map keys and
// values, pointers followed) by its printed name to its kind, as yaml.v3
// prints a target type with reflect.Type.String.
func kindsOf(t reflect.Type) map[string]reflect.Kind {
	out := map[string]reflect.Kind{}
	var visit func(reflect.Type)
	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if _, seen := out[t.String()]; seen {
			return
		}
		out[t.String()] = t.Kind()
		switch t.Kind() {
		case reflect.Struct:
			for i := range t.NumField() {
				visit(t.Field(i).Type)
			}
		case reflect.Slice, reflect.Array:
			visit(t.Elem())
		case reflect.Map:
			visit(t.Key())
			visit(t.Elem())
		default:
		}
	}
	visit(t)
	return out
}
