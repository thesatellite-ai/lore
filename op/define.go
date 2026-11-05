package op

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Entry is the type-erased view of an operation that surfaces, the runner
// and the docs generator hold. It never exposes an `any`.
type Entry interface {
	Spec() Spec
	InputFields() []FieldInfo
	OutputFields() []FieldInfo
	// Decode fills a fresh input struct through d.
	Decode(d Decoder) (Input, error)
	// Invoke runs the operation's function on a decoded input. Only a
	// Runner calls it, after its checks; a surface that called Invoke
	// directly would bypass the scanner and the audit log.
	Invoke(ctx context.Context, in Input) (Result, error)
}

// Op is one operation with typed input In and output Out.
type Op[In, Out any] struct {
	spec Spec
	run  func(context.Context, In) (Out, error)
	in   []FieldInfo
	out  []FieldInfo
}

var _ Entry = (*Op[struct{}, struct{}])(nil)

// Define builds an operation from its spec and typed run function. It reads
// In's and Out's struct tags once (arg, flag, json, desc, ui) and panics on
// an invalid name or tag: those are programmer errors that must stop the
// binary at startup, never surface to a user at run time.
func Define[In, Out any](spec Spec, run func(context.Context, In) (Out, error)) *Op[In, Out] {
	if !ValidName(spec.Name) {
		panic(tagError{fmt.Sprintf("op.Define: invalid operation name %q (want noun.verb, snake_case)", spec.Name)})
	}
	if run == nil {
		panic(tagError{"op.Define(" + spec.Name + "): nil run function"})
	}
	in := fieldsOf(spec.Name, reflect.TypeFor[In](), dirInput)
	checkGroups(spec.Name, in)
	return &Op[In, Out]{
		spec: spec,
		run:  run,
		in:   in,
		out:  fieldsOf(spec.Name, reflect.TypeFor[Out](), dirOutput),
	}
}

// Spec implements Entry.
func (o *Op[In, Out]) Spec() Spec { return o.spec }

// InputFields implements Entry.
func (o *Op[In, Out]) InputFields() []FieldInfo { return o.in }

// OutputFields implements Entry.
func (o *Op[In, Out]) OutputFields() []FieldInfo { return o.out }

// Decode implements Entry.
func (o *Op[In, Out]) Decode(d Decoder) (Input, error) {
	ptr := reflect.New(reflect.TypeFor[In]())
	if err := d.Decode(ptr.Elem(), o.in); err != nil {
		return Input{}, err
	}
	if err := checkRequired(ptr.Elem(), o.in); err != nil {
		return Input{}, err
	}
	return Input{v: ptr.Elem(), fields: o.in, op: o.spec.Name}, nil
}

// Invoke implements Entry.
func (o *Op[In, Out]) Invoke(ctx context.Context, in Input) (Result, error) {
	if in.op != o.spec.Name || in.v.Type() != reflect.TypeFor[In]() {
		return Result{}, errs.New(errs.Internal, "op: input of "+in.op+" passed to "+o.spec.Name)
	}
	// reflect.Value.Interface returns `any` (stdlib signature); the type
	// check above makes this assertion total, and the value never leaves
	// this line untyped.
	typed, ok := in.v.Interface().(In)
	if !ok {
		return Result{}, errs.New(errs.Internal, "op: input type mismatch for "+o.spec.Name)
	}
	out, err := o.run(ctx, typed)
	if err != nil {
		return Result{}, err
	}
	return Result{v: reflect.ValueOf(&out).Elem(), fields: o.out}, nil
}

// Input is a decoded input struct, read by the Runner (the scanner needs its
// text, the confirm rule its bool fields) without knowing its Go type.
type Input struct {
	v      reflect.Value
	fields []FieldInfo
	op     string
}

// TextValue is one piece of text in an input, for the secret scanner.
type TextValue struct {
	// Field is the json name; for a map entry, "name.key".
	Field string
	Text  string
}

// Texts returns every string the input carries: string fields, every item
// of a []string, every key and value of a map. The Runner scans them all.
func (i Input) Texts() []TextValue { return texts(i.v, i.fields, "") }

// texts collects the strings of struct v; prefix names the group a nested
// item belongs to ("children."), so every item of a repeated group is
// scanned under one stable field name (no index: allow rows key on field).
func texts(v reflect.Value, fields []FieldInfo, prefix string) []TextValue {
	var out []TextValue
	for _, f := range fields {
		fv := v.FieldByIndex(f.index)
		name := prefix + f.Name
		switch f.Type {
		case TypeString:
			out = append(out, TextValue{Field: name, Text: fv.String()})
		case TypeStrings:
			for j := range fv.Len() {
				out = append(out, TextValue{Field: name, Text: fv.Index(j).String()})
			}
		case TypeStringMap:
			iter := fv.MapRange()
			for iter.Next() {
				k := iter.Key().String()
				out = append(out, TextValue{Field: name, Text: k}, TextValue{Field: name + "." + k, Text: iter.Value().String()})
			}
		case TypeList:
			if f.Group { // the only input list; outputs are never scanned
				for j := range fv.Len() {
					out = append(out, texts(fv.Index(j), f.Fields, name+".")...)
				}
			}
		case TypeBool, TypeInt, TypeUint, TypeFloat, TypeDuration, TypeText, TypeObject:
		}
	}
	return out
}

// TargetNamer is an optional method of an input struct whose target store
// is not a `to` field the caller types but is named elsewhere (mission
// apply: the plan file's `to:`). The Runner reads it to attach a
// `<store>/<folder>` target before the run, exactly as for `--to`, so
// both spellings resolve the same way (BUG-T8). An error means the name
// cannot be read yet; the Runner leaves the reporting to the operation.
type TargetNamer interface {
	TargetName() (string, error)
}

// NamedTarget returns the target an input names through TargetNamer; ok
// is false when the input has no such method or it could not be read.
func (i Input) NamedTarget() (name string, ok bool) {
	if !i.v.IsValid() || !i.v.CanInterface() {
		return "", false
	}
	// reflect.Value.Interface returns the stdlib's untyped value; it is
	// narrowed to TargetNamer on this line and never kept untyped.
	t, isNamer := i.v.Interface().(TargetNamer)
	if !isNamer {
		return "", false
	}
	n, err := t.TargetName()
	if err != nil {
		return "", false
	}
	return n, true
}

// targetAliasKey carries the Runner's rewrite of a named target.
type targetAliasKey struct{}

// targetAlias is one rewrite: the name the input gave and the attached name.
type targetAlias struct{ from, to string }

// WithTargetAlias records that the target from is attached as to (a
// `<store>/<folder>` that names a store already attached at that folder),
// for an operation whose target is not a `to` field (TargetNamer).
func WithTargetAlias(ctx context.Context, from, to string) context.Context {
	return context.WithValue(ctx, targetAliasKey{}, targetAlias{from: from, to: to})
}

// TargetAlias returns the attached name of target name: the Runner's
// rewrite when it recorded one for name, else name itself.
func TargetAlias(ctx context.Context, name string) string {
	if a, ok := ctx.Value(targetAliasKey{}).(targetAlias); ok && a.from == name {
		return a.to
	}
	return name
}

// Bool returns the value of the bool field with json name name; ok is false
// when there is no such bool field.
func (i Input) Bool(name string) (value, ok bool) {
	for _, f := range i.fields {
		if f.Name == name && f.Type == TypeBool {
			return i.v.FieldByIndex(f.index).Bool(), true
		}
	}
	return false, false
}

// Op returns the name of the operation the input was decoded for.
func (i Input) Op() string { return i.op }

// Result is an operation's output, ready for a surface to print.
type Result struct {
	v      reflect.Value
	fields []FieldInfo
	// edit rewrites the human text (Texter) after the operation ran: the
	// Runner's step for something no single operation knows (a delete the
	// engine purged instead of trashing, P3-DEL-1). Nil = none.
	edit func(string) string
}

// WithTextEdit returns r whose human text (Text) is passed through edit
// after any edit already set. The JSON form is never edited: it carries the
// operation's own fields.
func (r Result) WithTextEdit(edit func(string) string) Result {
	if prev := r.edit; prev != nil {
		r.edit = func(s string) string { return edit(prev(s)) }
		return r
	}
	r.edit = edit
	return r
}

// Text returns the output's own human form (Texter) with the Runner's
// edits applied; ok is false when the output has none (the surface then
// prints its generic `name: value` form).
func (r Result) Text() (text string, ok bool) {
	if !r.v.IsValid() || !r.v.CanInterface() {
		return "", false
	}
	// Interface() is the reflect boundary; it is narrowed at once.
	t, isTexter := r.v.Interface().(Texter)
	if !isTexter {
		return "", false
	}
	text = t.Text()
	if r.edit != nil {
		text = r.edit(text)
	}
	return text, true
}

// Fields describes the output struct.
func (r Result) Fields() []FieldInfo { return r.fields }

// Value is the output struct, for table and text printers that walk Fields.
func (r Result) Value() reflect.Value { return r.v }

// JSON marshals the output struct (REST body, --json).
func (r Result) JSON() ([]byte, error) {
	if !r.v.IsValid() {
		return []byte("{}"), nil
	}
	// json.Marshal takes `any` (stdlib signature): the concrete output
	// struct goes straight in and nothing untyped comes back.
	b, err := json.Marshal(r.v.Interface())
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "op: encode result", err)
	}
	return b, nil
}

// checkRequired refuses a decoded input whose required fields are zero.
func checkRequired(v reflect.Value, fields []FieldInfo) error {
	for _, f := range fields {
		if f.Required && !f.AfterLookup && v.FieldByIndex(f.index).IsZero() {
			return errs.New(errs.InvalidInput, "missing required field "+f.Name)
		}
	}
	return nil
}
