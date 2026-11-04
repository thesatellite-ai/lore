// Package value is the closed sum type of every field value lore stores:
// one concrete type per Kind, each built only through a validating
// constructor, so a Value in hand is always well formed.
//
// Why closed: the cache builds typed columns, the validator checks fields
// against table definitions and the scanner walks text, all by switching on
// Kind. An interface with an unexported method means no package outside
// this one can add a kind behind their backs, and the exhaustive switches
// stay exhaustive.
//
// JSON: every Value marshals to its natural JSON (an ip is a string, a list
// an array). Decoding needs the kind, because "203.0.113.10" is text or an
// ip depending on the field: Parse(kind, raw) decodes against a known kind,
// Infer(raw) guesses the plain JSON kind when no schema is at hand (the
// envelope decoder), and a later schema-aware pass re-types with Parse.
package value

import (
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Kind names a value type. The strings are the `type` words of kind field
// rows and meta keys (spec 4.3), so they are a stored contract.
type Kind string

// The closed set of kinds.
const (
	KindText     Kind = "text"
	KindNumber   Kind = "number"
	KindBool     Kind = "bool"
	KindDate     Kind = "date"
	KindDateTime Kind = "datetime"
	KindDuration Kind = "duration"
	KindIP       Kind = "ip"
	KindCIDR     Kind = "cidr"
	KindDNS      Kind = "dns"
	KindURL      Kind = "url"
	KindEmail    Kind = "email"
	KindJSON     Kind = "json"
	KindList     Kind = "list"
	KindRef      Kind = "ref"
	KindNull     Kind = "null"
)

// Kinds lists every kind in declaration order.
func Kinds() []Kind {
	return []Kind{
		KindText, KindNumber, KindBool, KindDate, KindDateTime, KindDuration,
		KindIP, KindCIDR, KindDNS, KindURL, KindEmail, KindJSON, KindList,
		KindRef, KindSecretRef, KindNull,
	}
}

// ParseKind validates a stored kind word.
func ParseKind(s string) (Kind, error) {
	for _, k := range Kinds() {
		if string(k) == s {
			return k, nil
		}
	}
	return "", errs.New(errs.InvalidInput, "value: unknown kind "+quote(s))
}

// Value is one field value. Implemented only by this package's types.
type Value interface {
	// Kind reports which concrete type this is.
	Kind() Kind
	// MarshalJSON returns the canonical compact JSON of the value.
	MarshalJSON() ([]byte, error)
	// String is the plain text form (what a person types on the CLI).
	String() string
	sealed()
}

// Equal reports whether a and b are the same kind with the same canonical
// JSON. Two nil Values are equal; nil never equals a non-nil Value.
func Equal(a, b Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Kind() != b.Kind() {
		return false
	}
	ab, aerr := a.MarshalJSON()
	bb, berr := b.MarshalJSON()
	return aerr == nil && berr == nil && string(ab) == string(bb)
}

// maxQuoted bounds how much of a rejected input an error repeats, so a
// pasted blob (or a secret pasted by mistake) is not echoed back whole.
const maxQuoted = 48

// quote renders a rejected input for an error message, truncated.
func quote(s string) string {
	r := []rune(s)
	if len(r) > maxQuoted {
		return strconv.Quote(string(r[:maxQuoted])) + "…"
	}
	return strconv.Quote(s)
}

// invalid builds the constructor error. It never repeats the rejected input:
// this package cannot know whether the field is sensitive, and sensitive
// values must never reach an error object (CLI_REFERENCE 1.4). Callers above
// (the validator) rewrap it as E_RESOURCE_INVALID or E_META_TYPE with the
// field name.
func invalid(k Kind, why string) error {
	return errs.New(errs.InvalidInput, "value: not a valid "+string(k)+": "+why)
}
