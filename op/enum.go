package op

import (
	"fmt"
	"reflect"
	"slices"
)

// Enumerated is implemented by a named string type whose values are a
// closed set (onboard.Kind, onboard.Status, ...). An operation field of
// that type, or a []T of it, gets FieldInfo.Enum from EnumValues, and the
// OpenAPI document lists the set as the field's `enum`, so a client (the
// setup page's contract test) can compare its own value set with the Go
// constants without a hand-copied list.
//
// Invariant: EnumValues returns every value the type's constants define,
// each once, none empty, and is the one source of truth for the set: a
// new constant must be added there (the type's own test pins that), and
// the document then changes with it.
type Enumerated interface {
	EnumValues() []string
}

var enumeratedType = reflect.TypeFor[Enumerated]()

// enumOf is the value set of a field of Go type t: t's EnumValues, or its
// element's for a slice of strings; nil when t is not Enumerated. A
// duplicate or empty value is a programmer error (tagError), caught at
// Define time like a struct-tag mistake.
func enumOf(where string, t reflect.Type) []string {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.String || !t.Implements(enumeratedType) {
		return nil
	}
	vals := reflect.Zero(t).Interface().(Enumerated).EnumValues()
	if len(vals) == 0 {
		panic(tagError{fmt.Sprintf("%s: %s.EnumValues is empty", where, t)})
	}
	for i, v := range vals {
		if v == "" || slices.Contains(vals[:i], v) {
			panic(tagError{fmt.Sprintf("%s: %s.EnumValues has an empty or repeated value %q", where, t, v)})
		}
	}
	return slices.Clone(vals)
}
