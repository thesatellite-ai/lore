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
	return &Op[In, Out]{
		spec: spec,
		run:  run,
		in:   fieldsOf(spec.Name, reflect.TypeFor[In](), dirInput),
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
func (i Input) Texts() []TextValue {
	var out []TextValue
	for _, f := range i.fields {
		fv := i.v.FieldByIndex(f.index)
		switch f.Type {
		case TypeString:
			out = append(out, TextValue{Field: f.Name, Text: fv.String()})
		case TypeStrings:
			for j := range fv.Len() {
				out = append(out, TextValue{Field: f.Name, Text: fv.Index(j).String()})
			}
		case TypeStringMap:
			iter := fv.MapRange()
			for iter.Next() {
				k := iter.Key().String()
				out = append(out, TextValue{Field: f.Name, Text: k}, TextValue{Field: f.Name + "." + k, Text: iter.Value().String()})
			}
		case TypeBool, TypeInt, TypeUint, TypeFloat, TypeDuration, TypeText, TypeObject, TypeList:
		}
	}
	return out
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
