package op

import (
	"reflect"
	"strings"
)

// labelJSON is the json name of a record's store label (v0.1.17's
// `_label`: "[<store>/<folder>]" for a row of another store than the
// repo's, "" for a repo row).
const labelJSON = "_label"

// showLabelDepth bounds the walk for the label below the output's data
// (a record embedding the record it extends: task.Full, work.Record).
const showLabelDepth = 3

// RowLabel is the `_label` of the row a show output carries in its `data`
// field ("" when the output has none, or the row is the repo's own).
func (r Result) RowLabel() string {
	v := r.v
	for v.IsValid() && v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return ""
	}
	for i := range v.NumField() {
		if jsonName(v.Type().Field(i)) == "data" {
			return labelIn(v.Field(i), 0)
		}
	}
	return ""
}

// WithShowLabel is r whose human text names the store/folder holding its
// row on the first line, after the id (devco D-557): memory and rule show
// printed "[<org>/<folder>]" there, every other noun's show printed no
// store at all, so a row read from the home folder said nothing of where
// it was. A first line already naming the label is left as it is.
func (r Result) WithShowLabel() Result {
	label := r.RowLabel()
	if label == "" {
		return r
	}
	return r.WithTextEdit(func(s string) string {
		first, rest, found := strings.Cut(s, "\n")
		if first == "" || strings.Contains(first, label) {
			return s
		}
		first += " " + label
		if !found {
			return first
		}
		return first + "\n" + rest
	})
}

// labelIn is the `_label` string field of struct v or of a struct it
// embeds or holds as a field, depth levels down at most.
func labelIn(v reflect.Value, depth int) string {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct || depth > showLabelDepth {
		return ""
	}
	t := v.Type()
	for i := range v.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if jsonName(f) == labelJSON && f.Type.Kind() == reflect.String {
			return v.Field(i).String()
		}
	}
	for i := range v.NumField() {
		// an embedded record may be of an unexported type; its promoted
		// _label is still read (String needs no exported access)
		if f := t.Field(i); f.Anonymous {
			if l := labelIn(v.Field(i), depth+1); l != "" {
				return l
			}
		}
	}
	return ""
}

// jsonName is f's json key ("" for "-" or no tag).
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "-" {
		return ""
	}
	return name
}
