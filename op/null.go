package op

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// jsonNull is the JSON literal an unset Null prints.
var jsonNull = []byte("null")

// Null is an optional output value that is always printed: JSON null when
// unset, the value otherwise. Op outputs hold no pointers, and omitempty
// drops the key, so a field v0.1.17 printed as `"plan": null` (a script
// reads the key) is a Null in the output struct. The zero Null is unset.
type Null[T comparable] struct {
	v   T
	set bool
}

// Some returns a set Null holding v.
func Some[T comparable](v T) Null[T] { return Null[T]{v: v, set: true} }

// SomeIf is Some(v) when v is not T's zero value, else the unset Null
// (the usual case: an empty brief means "no such relation").
func SomeIf[T comparable](v T) Null[T] {
	var zero T
	if v == zero {
		return Null[T]{}
	}
	return Some(v)
}

// Get returns the value and whether it is set.
func (n Null[T]) Get() (T, bool) { return n.v, n.set }

// MarshalJSON prints null when unset, the value otherwise.
func (n Null[T]) MarshalJSON() ([]byte, error) {
	if !n.set {
		return jsonNull, nil
	}
	return json.Marshal(n.v)
}

// UnmarshalJSON reads null as unset and anything else as the value.
func (n *Null[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), jsonNull) {
		*n = Null[T]{}
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = Some(v)
	return nil
}

// nullable is implemented by every Null[T]: Define reads T's type to
// describe the field (FieldInfo.Nullable).
type nullable interface{ nullElem() reflect.Type }

func (Null[T]) nullElem() reflect.Type { return reflect.TypeFor[T]() }
