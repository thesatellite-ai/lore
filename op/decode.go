package op

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

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
		return bodyError(err)
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
	// FlagSeq holds, per flag, the order each of its values was typed in
	// among every flag value of the command line (same order as Flags). A
	// repeated group whose items are given by its flag (task split's
	// --title, U-4) reads it to give a flag typed after an item to that
	// item; nil keeps every value on the outer input.
	FlagSeq map[string][]int
}

// Decode implements Decoder.
//
// A positional field given by its flag (U-4: `--id tsk_…`, the canonical
// form) takes the flag's value; the positionals typed fill the other
// positional fields in order, so the v0.1.17 form `lore task show tsk_…`
// and a mix (`lore config set --key a.b true`) both decode. A field given
// both ways is refused.
func (d ArgsDecoder) Decode(dst reflect.Value, fields []FieldInfo) error {
	slot, err := d.slots(fields)
	if err != nil {
		return err
	}
	flags := d.Flags
	var items []map[string][]string
	var heads []string // a group's items given by its flag (U-4)
	for _, f := range fields {
		if f.Group { // at most one: a group is the rest positional
			at, ok := slot[f.Name]
			if !ok {
				continue
			}
			if vals, given := flags[f.ArgOf]; given && f.ArgOf != "" && at >= len(d.Args) && d.FlagSeq != nil {
				heads = vals
				if flags, items, err = d.routeGroupByFlag(f); err != nil {
					return err
				}
				continue
			}
			g := f
			g.Arg = at
			if flags, items, err = d.routeGroup(g); err != nil {
				return err
			}
		}
	}
	used := map[string]bool{}
	slots := 0
	rest := false
	for _, f := range fields {
		var vals []string
		_, byFlag := flags[f.Flag]
		switch {
		case f.Group && heads != nil:
			rest = true
			if err := decodeGroup(dst.FieldByIndex(f.index), f, heads, items); err != nil {
				return err
			}
			continue
		case f.Arg != NoArg && !(f.Flag != "" && byFlag):
			at, ok := slot[f.Name]
			if !ok {
				continue
			}
			slots = max(slots, at+1)
			if at >= len(d.Args) {
				continue
			}
			vals = d.Args[at : at+1]
			if f.Group {
				rest = true
				if err := decodeGroup(dst.FieldByIndex(f.index), f, d.Args[at:], items); err != nil {
					return err
				}
				continue
			}
			if f.Rest {
				rest = true
				vals = []string{strings.Join(d.Args[at:], " ")}
				if f.Type == TypeStrings { // one value per argument (FB-TASK-2)
					vals = d.Args[at:]
				}
			}
		case f.Flag != "":
			v, ok := flags[f.Flag]
			if !ok {
				continue
			}
			used[f.Flag] = true
			vals = v
			if emptyValues(v) && f.Type != TypeBool {
				if what := emptyWant(f); what != "" {
					return errs.New(errs.InvalidInput, "--"+f.Flag+" is empty; give "+what).
						WithHint("give a value, or leave --" + f.Flag + " out")
				}
			}
		default:
			continue
		}
		if err := setField(dst.FieldByIndex(f.index), f, vals); err != nil {
			return err
		}
	}
	if !rest && len(d.Args) > slots {
		return errs.New(errs.InvalidInput, "unexpected value "+strconv.Quote(d.Args[slots])).
			WithHint("give every value with its flag (--name \"value\"); the command's --help lists its flags")
	}
	for name := range flags {
		if !used[name] {
			return errs.New(errs.InvalidInput, "unknown flag --"+name)
		}
	}
	return nil
}

// slots maps each positional field not given by its flag to the index of
// the typed positional it reads, in argument order. A positional typed for
// a field whose flag is also given is refused (`lore task show tsk_a --id
// tsk_b`): only when no field is left for it, so a mix still decodes.
func (d ArgsDecoder) slots(fields []FieldInfo) (map[string]int, error) {
	args := make([]FieldInfo, 0, len(fields))
	for _, f := range fields {
		if f.Arg != NoArg {
			args = append(args, f)
		}
	}
	slices.SortFunc(args, func(a, b FieldInfo) int { return a.Arg - b.Arg })
	out := map[string]int{}
	n := 0
	var byFlag []FieldInfo
	for _, f := range args {
		if _, ok := d.Flags[f.Flag]; f.Flag != "" && ok {
			byFlag = append(byFlag, f)
			continue
		}
		out[f.Name] = n
		n++
	}
	if len(byFlag) > 0 && len(d.Args) > n && !restIn(args, out) {
		f := byFlag[0]
		return nil, errs.New(errs.InvalidInput, "--"+f.Flag+" is given twice: as --"+f.Flag+" and as the value "+strconv.Quote(d.Args[n])).
			WithHint("give it once, as --" + f.Flag + " \"value\"")
	}
	return out, nil
}

// restIn reports whether a rest positional still reads typed arguments.
func restIn(args []FieldInfo, slot map[string]int) bool {
	for _, f := range args {
		if _, ok := slot[f.Name]; ok && f.Rest {
			return true
		}
	}
	return false
}

// emptyWant is what a flag given with only blank values must name instead,
// "" when an empty value is fine: the field's nonempty tag (U-3), or a
// required positional's flag (`--id ""` names no row; U-4).
func emptyWant(f FieldInfo) string {
	switch {
	case f.NonEmpty != "":
		return f.NonEmpty
	case f.Named && f.Required && !f.AfterLookup:
		return "the " + strings.ReplaceAll(f.Name, "_", " ")
	}
	return ""
}

// emptyValues reports a flag given with only blank values (`--tasklist=`,
// `--tasklist ""`): for a NonEmpty field that is a mistake, not "unset".
func emptyValues(vals []string) bool {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
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
	bad := func(want string) error {
		return errs.New(errs.InvalidInput, label+": want "+want).WithHint("pass " + label + " " + want)
	}
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
			// The cause is the only text that says what is wrong (a JSON
			// syntax error, a wrong id prefix); keep it in the message. The
			// code stays E_INVALID_INPUT; a lore cause gives its message
			// (without its code) and its hint.
			why, hint := err.Error(), "the command's --help shows the value "+label+" takes"
			var le *errs.Error
			if errors.As(err, &le) && le.Message != "" {
				why = le.Message
				if le.Hint != "" {
					hint = le.Hint
				}
			}
			return errs.Wrap(errs.InvalidInput, label+": invalid value: "+why, err).WithHint(hint)
		}
	case TypeStrings, TypeStringMap, TypeObject, TypeList:
	}
	return nil
}

// jsonUnknownField is the prefix encoding/json gives the error of a member
// DisallowUnknownFields refused (it has no error type of its own).
const jsonUnknownField = "json: unknown field "

// bodyFieldsHint tells a REST or --json caller where the field names are.
const bodyFieldsHint = "the operation's fields are listed in GET /openapi.json (or lore <command> --help)"

// bodyError turns a JSON decoding failure into a refusal that names what is
// wrong: the unknown member, the member with the wrong type (and the type
// it takes), or the JSON syntax error. A bare "does not fit" made an agent
// guess which of its members the operation refused.
func bodyError(err error) error {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case strings.HasPrefix(err.Error(), jsonUnknownField):
		return errs.Wrap(errs.InvalidInput, "unknown field "+strings.TrimPrefix(err.Error(), jsonUnknownField), err).WithHint(bodyFieldsHint)
	case errors.As(err, &typeErr) && typeErr.Field != "":
		return errs.Wrap(errs.InvalidInput, "field "+strconv.Quote(jsonFieldPath(typeErr.Field))+" takes "+jsonKindName(typeErr.Type)+", not a JSON "+typeErr.Value, err).WithHint(bodyFieldsHint)
	case errors.As(err, &syntaxErr):
		return errs.Wrap(errs.InvalidInput, "the request body is not valid JSON (byte "+strconv.FormatInt(syntaxErr.Offset, 10)+": "+syntaxErr.Error()+")", err)
	}
	return errs.Wrap(errs.InvalidInput, "the request body does not fit the operation's input", err)
}

// jsonFieldPath is a decode error's field as the request names it: some Go
// releases (1.26) put an embedded struct's Go name in front
// (LiveFlags.live), which is no member of the body. Members are snake_case,
// so a segment starting with a capital is a Go type name.
func jsonFieldPath(field string) string {
	segs := strings.Split(field, ".")
	kept := make([]string, 0, len(segs))
	for _, s := range segs {
		if s != "" && unicode.IsUpper([]rune(s)[0]) {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		return field
	}
	return strings.Join(kept, ".")
}

// jsonKindName names the JSON value a Go field type decodes from, in the
// words a REST caller uses.
func jsonKindName(t reflect.Type) string {
	if t == reflect.TypeFor[time.Duration]() {
		return "integer nanoseconds"
	}
	// ids, timestamps and other text-decoded values travel as strings.
	if reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		return "a string"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "an integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "a non-negative integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "an object"
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.Interface, reflect.Pointer, reflect.UnsafePointer:
	}
	return "a " + t.String()
}
