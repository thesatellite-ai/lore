package op

// topfield.go — top-level JSON fields the Runner adds to an operation's
// output, which no single operation knows (devco D-623). A read in an
// unlinked own repo that a resource of the org routes to <org>/<folder>
// printed that folder as a text line only; its --json carried no field
// naming where it looked, so an empty answer was silent about it. The
// composition root declares such fields per operation on the registry
// (WithTopFields) so the OpenAPI document promises them, and sets the value
// on the Result (WithTopText) so --json and REST carry it.

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
)

// RoutedField is the top-level output field naming the <org>/<folder> a
// read was routed to when it ran on a store folder it did not name
// (wire's resource-routed reads); absent on every other run.
const RoutedField = "routed"

// topText is one top-level text field a Result's JSON form carries in
// addition to the operation's own fields.
type topText struct {
	name, text string
}

// WithTopText returns r whose JSON form also carries the top-level text
// field name = text (put first, before the operation's own fields). An
// empty text, or a name the output already has, leaves the JSON as is: the
// operation's own field always wins.
func (r Result) WithTopText(name, text string) Result {
	if text == "" || slices.ContainsFunc(r.fields, func(f FieldInfo) bool { return f.Name == name }) {
		return r
	}
	r.top = append(slices.Clone(r.top), topText{name: name, text: text})
	return r
}

// spliceTop puts r's top-level text fields at the front of b, the
// marshaled output object; b is returned as is when it is no object.
func (r Result) spliceTop(b []byte) ([]byte, error) {
	if len(r.top) == 0 || len(b) < 2 || b[0] != '{' {
		return b, nil
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, t := range r.top {
		k, err := json.Marshal(t.name)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "op: encode result field", err)
		}
		v, err := json.Marshal(t.text)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "op: encode result field", err)
		}
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(k)
		out.WriteByte(':')
		out.Write(v)
	}
	if rest := bytes.TrimSpace(b[1:]); len(rest) > 0 && rest[0] != '}' {
		out.WriteByte(',')
	}
	out.Write(b[1:])
	return out.Bytes(), nil
}

// WithTopFields returns a copy of r whose TopFields(name) is fields(spec)
// for each operation: the output fields the composition root adds to an
// operation's result (WithTopText) beyond its own, for the OpenAPI
// document. r itself is unchanged (a Registry stays immutable).
func (r *Registry) WithTopFields(fields func(Spec) []FieldInfo) *Registry {
	c := &Registry{byName: r.byName, names: r.names, top: map[string][]FieldInfo{}}
	for _, n := range r.names {
		if fs := fields(r.byName[n].Spec()); len(fs) > 0 {
			c.top[n] = fs
		}
	}
	return c
}

// TopFields are the output fields the composition root adds to the
// operation name's result (WithTopFields); nil for none.
func (r *Registry) TopFields(name string) []FieldInfo { return r.top[name] }
