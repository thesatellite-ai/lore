package op

import (
	"encoding"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/schema"
)

// FieldType is the closed set of field shapes an operation struct may use.
// Surfaces switch on it to build flags, JSON schemas and form widgets.
type FieldType string

// Field types.
const (
	TypeString    FieldType = "string"
	TypeBool      FieldType = "bool"
	TypeInt       FieldType = "int"
	TypeUint      FieldType = "uint"
	TypeFloat     FieldType = "float"
	TypeDuration  FieldType = "duration"
	TypeStrings   FieldType = "strings"    // []string: a repeatable flag
	TypeStringMap FieldType = "string_map" // map[string]string: repeated key=value
	// TypeText is any type that parses itself from text (an ids.ID, for
	// instance): encoding.TextUnmarshaler on the pointer.
	TypeText FieldType = "text"
	// TypeObject and TypeList (of objects) appear only in outputs; Fields
	// holds the children.
	TypeObject FieldType = "object"
	TypeList   FieldType = "list"
)

// FieldInfo describes one struct field, read once by reflection at Define
// time.
type FieldInfo struct {
	// Name is the json name: the REST and --json field.
	Name string
	Type FieldType
	// Arg is the positional CLI argument index, or NoArg.
	Arg int
	// Rest marks the last positional string argument that takes every
	// remaining argument joined by single spaces (`rest:"true"`), as
	// v0.1.17 joined `lore reminder add --due X hello world`.
	Rest bool
	// Flag is the CLI flag name ("" when the field is positional or has no
	// flag).
	Flag string
	Desc string
	// Required: the json tag has no omitempty. Bool fields are never
	// required (false is a value).
	Required bool
	UI       schema.UI
	// Fields are the children of an object or list-of-object output field.
	Fields []FieldInfo
	// Nullable marks an op.Null output field: printed as JSON null when
	// unset (Type and Fields describe the value when set).
	Nullable bool
	// Actor marks an input field of Go type op.ActorRef: its value names an
	// actor (@handle, stable key or act_ id). XQ8: a flag named `by` must be
	// one (CheckFlagWords).
	Actor bool

	index []int
}

// NoArg marks a field that is not a positional argument.
const NoArg = -1

// Struct tag keys read by Define.
const (
	tagJSON = "json"
	tagArg  = "arg"
	tagFlag = "flag"
	tagDesc = "desc"
	tagUI   = "ui"
	tagRest = "rest"
)

// ui tag keys.
const (
	uiLabel  = "label"
	uiWidget = "widget"
	uiInList = "in_list"
)

// flagName is a CLI flag: lowercase words joined by hyphens.
var flagName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// fieldDirection says whether a struct is an input or an output: outputs may
// nest objects, inputs may not (a flag cannot hold a struct).
type fieldDirection int

const (
	dirInput fieldDirection = iota
	dirOutput
)

var (
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
	textMarshaler   = reflect.TypeFor[encoding.TextMarshaler]()
	durationType    = reflect.TypeFor[time.Duration]()
	actorRefType    = reflect.TypeFor[ActorRef]()
)

// tagError is the panic value of a struct-tag mistake. Define panics on
// these because they are programmer errors fixed before the binary ships;
// a panic at startup (and in every test that builds the registry) is the
// fastest possible signal.
type tagError struct{ msg string }

func (e tagError) Error() string { return e.msg }

// fieldsOf reads the fields of struct type t.
func fieldsOf(op string, t reflect.Type, dir fieldDirection) []FieldInfo {
	if t.Kind() != reflect.Struct {
		panic(tagError{fmt.Sprintf("op.Define(%s): %s is not a struct", op, t)})
	}
	var out []FieldInfo
	args := map[int]string{}
	names := map[string]bool{}
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		f, skip := fieldOf(op, sf, dir)
		if skip {
			continue
		}
		where := fmt.Sprintf("op.Define(%s): field %s", op, sf.Name)
		if names[f.Name] {
			panic(tagError{where + ": json name " + f.Name + " used twice"})
		}
		names[f.Name] = true
		if f.Arg != NoArg {
			if prev, dup := args[f.Arg]; dup {
				panic(tagError{where + ": arg " + strconv.Itoa(f.Arg) + " also used by " + prev})
			}
			args[f.Arg] = f.Name
		}
		f.index = sf.Index
		out = append(out, f)
	}
	for i := range len(args) {
		if _, ok := args[i]; !ok {
			panic(tagError{fmt.Sprintf("op.Define(%s): positional args must be 0..%d with no gap", op, len(args)-1)})
		}
	}
	for _, f := range out {
		if f.Rest && f.Arg != len(args)-1 {
			panic(tagError{fmt.Sprintf("op.Define(%s): rest argument %s must be the last positional", op, f.Name)})
		}
	}
	return out
}

// fieldOf reads one field; skip is true for `json:"-"`.
func fieldOf(op string, sf reflect.StructField, dir fieldDirection) (FieldInfo, bool) {
	where := fmt.Sprintf("op.Define(%s): field %s", op, sf.Name)
	jsonTag, hasJSON := sf.Tag.Lookup(tagJSON)
	if !hasJSON {
		panic(tagError{where + ": needs a json tag"})
	}
	name, opts, _ := strings.Cut(jsonTag, ",")
	if name == "-" {
		return FieldInfo{}, true
	}
	if name == "" {
		panic(tagError{where + ": json tag has no name"})
	}
	f := FieldInfo{Name: name, Arg: NoArg, Desc: sf.Tag.Get(tagDesc)}
	ft := sf.Type
	if n, ok := reflect.Zero(ft).Interface().(nullable); ok {
		if dir == dirInput {
			panic(tagError{where + ": op.Null is an output type"})
		}
		ft, f.Nullable = n.nullElem(), true
	}
	f.Type, f.Fields = typeOf(op, where, ft, dir)
	f.Actor = ft == actorRefType
	f.Required = !strings.Contains(opts, "omitempty") && f.Type != TypeBool
	if a, ok := sf.Tag.Lookup(tagArg); ok {
		n, err := strconv.Atoi(a)
		if err != nil || n < 0 {
			panic(tagError{where + ": arg must be a non-negative index"})
		}
		f.Arg = n
	}
	if r, ok := sf.Tag.Lookup(tagRest); ok {
		if r != "true" || f.Arg == NoArg || f.Type != TypeString {
			panic(tagError{where + `: rest:"true" goes on a positional string argument`})
		}
		f.Rest = true
	}
	if fl, ok := sf.Tag.Lookup(tagFlag); ok {
		if !flagName.MatchString(fl) {
			panic(tagError{where + ": flag " + strconv.Quote(fl) + " is not lowercase-with-hyphens"})
		}
		if f.Arg != NoArg {
			panic(tagError{where + ": a field is either an arg or a flag"})
		}
		f.Flag = fl
	}
	if dir == dirInput && (f.Type == TypeObject || f.Type == TypeList) {
		panic(tagError{where + ": inputs cannot nest structs"})
	}
	if ui, ok := sf.Tag.Lookup(tagUI); ok {
		f.UI = parseUI(where, ui)
	}
	return f, false
}

// typeOf maps a Go type to its FieldType.
func typeOf(op, where string, t reflect.Type, dir fieldDirection) (FieldType, []FieldInfo) {
	if reflect.PointerTo(t).Implements(textUnmarshaler) && t.Implements(textMarshaler) {
		return TypeText, nil
	}
	if t == durationType {
		return TypeDuration, nil
	}
	switch t.Kind() {
	case reflect.String:
		return TypeString, nil
	case reflect.Bool:
		return TypeBool, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return TypeInt, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return TypeUint, nil
	case reflect.Float32, reflect.Float64:
		return TypeFloat, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return TypeStrings, nil
		}
		if dir == dirOutput && t.Elem().Kind() == reflect.Struct {
			return TypeList, fieldsOf(op, t.Elem(), dir)
		}
	case reflect.Map:
		if t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String {
			return TypeStringMap, nil
		}
	case reflect.Struct:
		if dir == dirOutput {
			return TypeObject, fieldsOf(op, t, dir)
		}
	default:
	}
	panic(tagError{where + ": unsupported type " + t.String()})
}

// parseUI reads `ui:"label=…,widget=…,in_list=…"`.
func parseUI(where, tag string) schema.UI {
	var ui schema.UI
	for kv := range strings.SplitSeq(tag, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			panic(tagError{where + ": ui entry " + strconv.Quote(kv) + " is not key=value"})
		}
		switch k {
		case uiLabel:
			ui.Label = v
		case uiWidget:
			if !schema.Widget(v).Valid() {
				panic(tagError{where + ": unknown widget " + strconv.Quote(v)})
			}
			ui.Widget = schema.Widget(v)
		case uiInList:
			b, err := strconv.ParseBool(v)
			if err != nil {
				panic(tagError{where + ": in_list must be true or false"})
			}
			ui.InList = schema.InListNo
			if b {
				ui.InList = schema.InListYes
			}
		default:
			panic(tagError{where + ": unknown ui key " + strconv.Quote(k)})
		}
	}
	return ui
}
