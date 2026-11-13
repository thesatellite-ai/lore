package op

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
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
	// TypeObject and TypeList (of objects) appear in outputs; Fields holds
	// the children. An input holds a TypeList only as a repeated group
	// (group.go, P3-TASK-1).
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
	// Rest marks the last positional argument that takes every remaining
	// argument (`rest:"true"`): a string field joins them by single spaces,
	// as v0.1.17 joined `lore reminder add --due X hello world`; a []string
	// field keeps one value per argument (`lore task split <id> "a" "b"`,
	// FB-TASK-2).
	Rest bool
	// Group marks a repeated group input (group.go): a rest positional of
	// type []S (Type TypeList) whose Fields are the item's fields, one
	// positional head at arg 0 and flags that override the outer input's
	// shared flags of the same name for that item.
	Group bool
	// Flag is the CLI flag name ("" when the field has no flag). A
	// positional field has one too (U-4, named args everywhere): its
	// `flag` tag, else its json name in kebab case (namePositionals); the
	// positional stays a quiet alias, shown nowhere.
	Flag string
	// Named marks a positional field whose canonical CLI form is its flag
	// (U-4): help, usage and docs show `--flag <value>`, never the
	// positional.
	Named bool
	// ArgOf names the flag a positional is a quiet alias of (`argof:"body"`:
	// memory add's text is --body): the field takes no flag of its own and
	// is shown nowhere; the operation merges it into that flag's value.
	ArgOf string
	// Short is the one-letter CLI shorthand of Flag (`short:"n"` keeps
	// v0.1.17's `lore live log -n 5`); "" for none.
	Short string
	// Aliases are other CLI spellings of Flag (`alias:"store"`): the store
	// flag is --to on the add commands (v0.1.17's spelling) and --store on
	// list and search, and each command takes both (FB-FLAG-STORE). The
	// CLI parses an alias into the same value; REST and --json keep Name.
	Aliases []string
	Desc    string
	// OwnJSON marks a TypeText field whose type also implements
	// json.Marshaler: its JSON form is the type's own (an object, a bool,
	// raw JSON), not the text string, so a schema must not promise a
	// string (setup.plan's answers, doctor's raw sync block).
	OwnJSON bool
	// Required: the json tag has neither omitempty nor omitzero, or the
	// field carries `required:"after-lookup"` (required.go). Bool fields
	// are never required (false is a value). omitzero counts like
	// omitempty: encoding/json leaves such a member out of the output, so
	// the OpenAPI document must not promise it.
	Required bool
	// AfterLookup marks a required field the operation checks itself after
	// its id lookup (OV29); the decoder does not refuse it (checkRequired).
	AfterLookup bool
	UI          schema.UI
	// Fields are the children of an object or list-of-object output field.
	Fields []FieldInfo
	// Nullable marks an op.Null output field: printed as JSON null when
	// unset (Type and Fields describe the value when set).
	Nullable bool
	// Actor marks an input field of Go type op.ActorRef: its value names an
	// actor (@handle, stable key or act_ id). XQ8: a flag named `by` must be
	// one (CheckFlagWords).
	Actor bool
	// Enum is the closed value set of a string or []string field whose Go
	// type is Enumerated (enum.go), in the type's order; nil otherwise.
	// surface/docs writes it as the JSON Schema `enum`.
	Enum []string
	// NonEmpty is what a CLI flag of the field must name when it is given
	// (`nonempty:"a tasklist id or name"`): `--tasklist=` with nothing is
	// refused, `--tasklist is empty; give a tasklist id or name`, instead
	// of reading as "not given" and silently taking the default (U-3: an
	// empty --tasklist attached the task to an auto-created default list).
	// "" lets an empty value through, as before.
	NonEmpty string

	index []int
}

// Spellings are every CLI name of the field's flag: Flag, then Aliases;
// nil for a field with no flag.
func (f FieldInfo) Spellings() []string {
	if f.Flag == "" {
		return nil
	}
	return append([]string{f.Flag}, f.Aliases...)
}

// LegacyFlagDesc starts the desc of a flag kept only so a v0.1.17 script
// still parses (it does nothing now). The CLI hides such a flag from
// --help: a newcomer must not read flags that exist only for old scripts.
const LegacyFlagDesc = "accepted for v0.1.17 scripts"

// Legacy reports a flag kept only for v0.1.17 scripts (LegacyFlagDesc).
func (f FieldInfo) Legacy() bool { return strings.HasPrefix(f.Desc, LegacyFlagDesc) }

// NoArg marks a field that is not a positional argument.
const NoArg = -1

// Struct tag keys read by Define.
const (
	tagJSON     = "json"
	tagArg      = "arg"
	tagFlag     = "flag"
	tagShort    = "short"
	tagDesc     = "desc"
	tagUI       = "ui"
	tagRest     = "rest"
	tagAlias    = "alias"
	tagNonEmpty = "nonempty"
	tagArgOf    = "argof"
)

// ui tag keys.
const (
	uiLabel  = "label"
	uiWidget = "widget"
	uiInList = "in_list"
)

// flagName is a CLI flag: lowercase words joined by hyphens.
var flagName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// shortName is a CLI flag shorthand: one lowercase letter.
var shortName = regexp.MustCompile(`^[a-z]$`)

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
	jsonMarshaler   = reflect.TypeFor[json.Marshaler]()
	durationType    = reflect.TypeFor[time.Duration]()
	timeType        = reflect.TypeFor[time.Time]()
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
	flags := map[string]string{} // CLI spelling (flag or alias) → field
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		var batch []FieldInfo
		if _, tagged := sf.Tag.Lookup(tagJSON); sf.Anonymous && !tagged && sf.Type.Kind() == reflect.Struct && dir == dirInput {
			// An embedded input struct with no json tag (a shared flag set
			// such as metaflag.Flags) adds its fields, as encoding/json
			// flattens it; positional args stay with the outer struct.
			for _, f := range fieldsOf(op, sf.Type, dir) {
				if f.Arg != NoArg {
					panic(tagError{fmt.Sprintf("op.Define(%s): embedded %s may not hold positional args", op, sf.Name)})
				}
				f.index = append(slices.Clone(sf.Index), f.index...)
				batch = append(batch, f)
			}
		} else {
			f, skip := fieldOf(op, sf, dir)
			if skip {
				continue
			}
			f.index = sf.Index
			batch = append(batch, f)
		}
		for _, f := range batch {
			where := fmt.Sprintf("op.Define(%s): field %s", op, f.Name)
			if names[f.Name] {
				panic(tagError{where + ": json name " + f.Name + " used twice"})
			}
			names[f.Name] = true
			for _, fl := range f.Spellings() {
				if prev, dup := flags[fl]; dup {
					panic(tagError{where + ": flag --" + fl + " also used by " + prev})
				}
				flags[fl] = f.Name
			}
			if f.Arg != NoArg {
				if prev, dup := args[f.Arg]; dup {
					panic(tagError{where + ": arg " + strconv.Itoa(f.Arg) + " also used by " + prev})
				}
				args[f.Arg] = f.Name
			}
			out = append(out, f)
		}
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
	f.OwnJSON = f.Type == TypeText && ownJSON(ft)
	f.Actor = ft == actorRefType
	if f.Type == TypeString || f.Type == TypeText || f.Type == TypeStrings {
		f.Enum = enumOf(where, ft)
	}
	f.Required = !jsonOmits(opts) && f.Type != TypeBool
	markAfterLookup(where, sf, &f)
	if a, ok := sf.Tag.Lookup(tagArg); ok {
		n, err := strconv.Atoi(a)
		if err != nil || n < 0 {
			panic(tagError{where + ": arg must be a non-negative index"})
		}
		f.Arg = n
	}
	if r, ok := sf.Tag.Lookup(tagRest); ok {
		if r != "true" || f.Arg == NoArg || (f.Type != TypeString && f.Type != TypeStrings && f.Type != TypeList) {
			panic(tagError{where + `: rest:"true" goes on a positional string, []string or []struct argument`})
		}
		f.Rest = true
		f.Group = f.Type == TypeList
	}
	if fl, ok := sf.Tag.Lookup(tagFlag); ok {
		if !flagName.MatchString(fl) {
			panic(tagError{where + ": flag " + strconv.Quote(fl) + " is not lowercase-with-hyphens"})
		}
		f.Flag = fl
	}
	if ao, ok := sf.Tag.Lookup(tagArgOf); ok {
		if f.Arg == NoArg || f.Flag != "" || !flagName.MatchString(ao) {
			panic(tagError{where + `: argof goes on a positional with no flag and names another field's flag`})
		}
		f.ArgOf = ao
	}
	if al, ok := sf.Tag.Lookup(tagAlias); ok {
		if f.Flag == "" {
			panic(tagError{where + `: alias goes with a flag`})
		}
		for a := range strings.SplitSeq(al, ",") {
			if !flagName.MatchString(a) || a == f.Flag || slices.Contains(f.Aliases, a) {
				panic(tagError{where + ": alias " + strconv.Quote(a) + " is not a new lowercase-with-hyphens flag name"})
			}
			f.Aliases = append(f.Aliases, a)
		}
	}
	if ne, ok := sf.Tag.Lookup(tagNonEmpty); ok {
		if (f.Flag == "" && f.Arg == NoArg) || ne == "" || f.Type != TypeString {
			panic(tagError{where + `: nonempty goes with a string flag and names what to give`})
		}
		f.NonEmpty = ne
	}
	if sh, ok := sf.Tag.Lookup(tagShort); ok {
		if f.Flag == "" || !shortName.MatchString(sh) {
			panic(tagError{where + `: short goes with a flag and is one lowercase letter`})
		}
		f.Short = sh
	}
	if dir == dirInput && (f.Type == TypeObject || (f.Type == TypeList && !f.Group)) {
		panic(tagError{where + `: inputs nest structs only as a repeated group (a []struct positional with rest:"true")`})
	}
	if ui, ok := sf.Tag.Lookup(tagUI); ok {
		f.UI = parseUI(where, ui)
	}
	return f, false
}

// ownJSON reports whether a text type's JSON form may be something other
// than a string: it implements json.Marshaler and either its zero value
// does not encode as a string (raw JSON → null, an answers map → {}) or it
// is a struct other than time.Time (an answer that is a string, a bool or
// a list). time.Time and the id types encode as strings.
func ownJSON(t reflect.Type) bool {
	if !t.Implements(jsonMarshaler) && !reflect.PointerTo(t).Implements(jsonMarshaler) {
		return false
	}
	if t.Kind() == reflect.Struct && t != timeType {
		return true
	}
	b, err := json.Marshal(reflect.Zero(t).Interface())
	return err != nil || len(b) == 0 || b[0] != '"'
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
		if t.Elem().Kind() == reflect.Struct {
			if dir == dirInput {
				return TypeList, groupItemFields(op, where, t.Elem())
			}
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

// jsonOmits reports whether a json tag's options let encoding/json leave
// the member out (omitempty or omitzero), so the member is never required.
func jsonOmits(opts string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == "omitempty" || o == "omitzero" {
			return true
		}
	}
	return false
}
