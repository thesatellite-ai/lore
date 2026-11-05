package op

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Decoder fills an operation's input struct from one surface's raw input.
// dst is the settable struct value; fields describe it. Surfaces pick a
// Decoder (JSONDecoder for REST and --json input, ArgsDecoder for the CLI)
// and hand it to the Runner; neither side ever holds the input as `any`.
type Decoder interface {
	Decode(dst reflect.Value, fields []FieldInfo) error
}

// JSONDecoder decodes a JSON object body. Unknown fields are refused so a
// typo in a REST body fails instead of being silently dropped.
type JSONDecoder struct{ Body []byte }

// Decode implements Decoder.
func (d JSONDecoder) Decode(dst reflect.Value, _ []FieldInfo) error {
	body := d.Body
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	// json.Decoder.Decode takes `any` (stdlib signature); the pointer to the
	// concrete input struct goes straight in.
	if err := dec.Decode(dst.Addr().Interface()); err != nil {
		return errs.Wrap(errs.InvalidInput, "the request body does not fit the operation's input", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errs.New(errs.InvalidInput, "the request body has data after the JSON object")
	}
	return nil
}

// ArgsDecoder decodes CLI positional arguments and flags. Flags maps a flag
// name to every value given for it (a repeatable flag has several); a bool
// flag given without a value is passed as "true" by the CLI surface.
type ArgsDecoder struct {
	Args  []string
	Flags map[string][]string
	// FlagPos holds, per flag, how many positional arguments came before
	// each of its values (same order as Flags). Only a repeated group
	// (group.go) reads it, to give a flag typed after an item to that item;
	// nil keeps every value on the outer input.
	FlagPos map[string][]int
}

// Decode implements Decoder.
func (d ArgsDecoder) Decode(dst reflect.Value, fields []FieldInfo) error {
	flags := d.Flags
	var items []map[string][]string
	for _, f := range fields {
		if f.Group { // at most one: a group is the rest positional
			var err error
			if flags, items, err = d.routeGroup(f); err != nil {
				return err
			}
		}
	}
	used := map[string]bool{}
	maxArg := NoArg
	rest := false
	for _, f := range fields {
		var vals []string
		switch {
		case f.Arg != NoArg:
			maxArg = max(maxArg, f.Arg)
			if f.Arg >= len(d.Args) {
				continue
			}
			vals = d.Args[f.Arg : f.Arg+1]
			if f.Group {
				rest = true
				if err := decodeGroup(dst.FieldByIndex(f.index), f, d.Args[f.Arg:], items); err != nil {
					return err
				}
				continue
			}
			if f.Rest {
				rest = true
				vals = []string{strings.Join(d.Args[f.Arg:], " ")}
				if f.Type == TypeStrings { // one value per argument (FB-TASK-2)
					vals = d.Args[f.Arg:]
				}
			}
		case f.Flag != "":
			v, ok := flags[f.Flag]
			if !ok {
				continue
			}
			used[f.Flag] = true
			vals = v
		default:
			continue
		}
		if err := setField(dst.FieldByIndex(f.index), f, vals); err != nil {
			return err
		}
	}
	if !rest && len(d.Args) > maxArg+1 {
		return errs.New(errs.InvalidInput, "too many arguments: want at most "+strconv.Itoa(maxArg+1))
	}
	for name := range flags {
		if !used[name] {
			return errs.New(errs.InvalidInput, "unknown flag --"+name)
		}
	}
	return nil
}

// setField parses vals into one field.
func setField(fv reflect.Value, f FieldInfo, vals []string) error {
	label := f.Name
	if f.Flag != "" {
		label = "--" + f.Flag
	}
	single := func() (string, error) {
		if len(vals) != 1 {
			return "", errs.New(errs.InvalidInput, label+" takes one value")
		}
		return vals[0], nil
	}
	bad := func(want string) error { return errs.New(errs.InvalidInput, label+": want "+want) }
	switch f.Type {
	case TypeStrings:
		fv.Set(reflect.AppendSlice(fv, reflect.ValueOf(vals)))
		return nil
	case TypeStringMap:
		if fv.IsNil() {
			fv.Set(reflect.MakeMap(fv.Type()))
		}
		for _, kv := range vals {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return bad("key=value")
			}
			fv.SetMapIndex(reflect.ValueOf(k).Convert(fv.Type().Key()), reflect.ValueOf(v).Convert(fv.Type().Elem()))
		}
		return nil
	case TypeObject, TypeList:
		return errs.New(errs.Internal, label+": not a CLI field")
	case TypeString, TypeBool, TypeInt, TypeUint, TypeFloat, TypeDuration, TypeText:
	}
	s, err := single()
	if err != nil {
		return err
	}
	switch f.Type {
	case TypeString:
		fv.SetString(s)
	case TypeBool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return bad("true or false")
		}
		fv.SetBool(b)
	case TypeInt:
		n, err := strconv.ParseInt(s, 10, fv.Type().Bits())
		if err != nil {
			return bad("an integer")
		}
		fv.SetInt(n)
	case TypeUint:
		n, err := strconv.ParseUint(s, 10, fv.Type().Bits())
		if err != nil {
			return bad("a non-negative integer")
		}
		fv.SetUint(n)
	case TypeFloat:
		n, err := strconv.ParseFloat(s, fv.Type().Bits())
		if err != nil {
			return bad("a number")
		}
		fv.SetFloat(n)
	case TypeDuration:
		dur, err := time.ParseDuration(s)
		if err != nil {
			return bad("a duration like 45m or 1h30m")
		}
		fv.SetInt(int64(dur))
	case TypeText:
		// Addr().Interface() returns `any` (stdlib signature); fieldOf only
		// classifies a field as TypeText when *T implements
		// encoding.TextUnmarshaler, so the assertion is total.
		u, ok := fv.Addr().Interface().(encoding.TextUnmarshaler)
		if !ok {
			return errs.New(errs.Internal, label+": not a text field")
		}
		if err := u.UnmarshalText([]byte(s)); err != nil {
			return errs.Wrap(errs.InvalidInput, label+": invalid value", err)
		}
	case TypeStrings, TypeStringMap, TypeObject, TypeList:
	}
	return nil
}
