// Package validate checks a row's field values against its table definition
// (schema.TableDef) before the engine stores them: unknown fields, missing
// required fields, closed value sets, and each value's kind. Values typed as
// text on a surface (a CLI flag, a form) are converted to the field's kind
// here, so "203.0.113.10" becomes an ip for an ip field and stays text for a
// text field.
//
// Layer L1: pure, no I/O; imports model and schema only.
//
// Invariant: no error from this package ever repeats a value, only field
// names and kinds, because the field may be sensitive and errors are printed
// verbatim to terminals, logs and HTTP clients (errs.Error invariant).
package validate

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Mode says what a set of values is.
type Mode string

// Modes.
const (
	// Whole: the complete field set of a live row (a create, or the result
	// of applying a patch). Every required field must be present.
	Whole Mode = "whole"
	// Patch: only the fields being changed. A required field may be left
	// out but never cleared (set to null).
	Patch Mode = "patch"
)

// Fields validates in against def and returns the typed values (text input
// converted to each field's kind). It reports every problem at once in one
// E_RESOURCE_INVALID error, so a person fixes a form in one pass.
//
// A null value means "clear this field": allowed for optional fields only.
// The returned map is new; in is never changed.
func Fields(def schema.TableDef, in map[string]value.Value, mode Mode) (map[string]value.Value, error) {
	out := make(map[string]value.Value, len(in))
	var problems []string
	for _, name := range sortedKeys(in) {
		f, ok := def.Field(name)
		if !ok {
			problems = append(problems, "unknown field "+name+" (allowed: "+strings.Join(allowed(def), ", ")+")")
			continue
		}
		v, err := Value(f, in[name])
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out[name] = v
	}
	if mode == Whole {
		for _, f := range def.Fields {
			if _, given := in[f.Name]; f.Required && !given { // a given value was checked by Value
				problems = append(problems, "field "+f.Name+" is required")
			}
		}
	}
	if len(problems) > 0 {
		return nil, errs.New(errs.ResourceInvalid, string(def.Name)+": "+strings.Join(problems, "; "))
	}
	return out, nil
}

// fieldErr is a problem with one field, phrased without its value.
type fieldErr struct{ msg string }

func (e fieldErr) Error() string { return e.msg }

// Value checks one value against its field and converts text input to the
// field's kind. Null passes for optional fields (it clears them). The error
// names the field and the wanted kind, never the value.
func Value(f schema.FieldDef, v value.Value) (value.Value, error) {
	if v == nil {
		return nil, fieldErr{"field " + f.Name + " has no value"}
	}
	if v.Kind() == value.KindNull {
		if f.Required {
			return nil, fieldErr{"field " + f.Name + " is required and cannot be cleared"}
		}
		return v, nil
	}
	typed, err := convert(f.Kind, f.Elem, v)
	if err != nil {
		return nil, fieldErr{"field " + f.Name + ": want " + want(f)}
	}
	if len(f.Values) > 0 && !slices.Contains(f.Values, typed.String()) {
		return nil, fieldErr{"field " + f.Name + ": want one of " + strings.Join(f.Values, ", ")}
	}
	return typed, nil
}

// convert returns v as kind k (lists: items of kind elem).
func convert(k, elem value.Kind, v value.Value) (value.Value, error) {
	if k == value.KindList {
		return convertList(elem, v)
	}
	if v.Kind() == k {
		return v, nil
	}
	t, isText := v.(value.Text)
	if !isText {
		return nil, errs.New(errs.InvalidInput, "kind mismatch")
	}
	return value.Parse(k, rawFor(k, t.String()))
}

// convertList accepts a list (items converted one by one) or a text holding
// a JSON array.
func convertList(elem value.Kind, v value.Value) (value.Value, error) {
	switch l := v.(type) {
	case value.List:
		items := l.Items()
		out := make([]value.Value, len(items))
		for i, it := range items {
			c, err := convert(elem, "", it)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return value.ListOf(elem, out...)
	case value.Text:
		return value.ParseList(elem, []byte(l.String()))
	}
	return nil, errs.New(errs.InvalidInput, "kind mismatch")
}

// rawFor is the JSON a text input stands for: the text itself as a JSON
// literal for number, bool and json fields ("42", "true", "{…}"), a JSON
// string for every string-shaped kind.
func rawFor(k value.Kind, s string) []byte {
	switch k {
	case value.KindNumber, value.KindBool, value.KindJSON:
		return []byte(s)
	case value.KindText, value.KindDate, value.KindDateTime, value.KindDuration, value.KindIP,
		value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef,
		value.KindSecretRef, value.KindList, value.KindNull:
	}
	return canon.String(s)
}

// want describes a field's expected value for an error message.
func want(f schema.FieldDef) string {
	if f.Kind == value.KindList {
		return "a list of " + string(f.Elem) + " values"
	}
	return "a " + string(f.Kind) + " value"
}

// allowed lists def's field names, sorted.
func allowed(def schema.TableDef) []string {
	out := make([]string, 0, len(def.Fields))
	for _, f := range def.Fields {
		out = append(out, f.Name)
	}
	slices.Sort(out)
	return out
}

// sortedKeys keeps error text stable whatever the map order.
func sortedKeys(m map[string]value.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
