package cli

import (
	"strings"

	"github.com/thesatellite-ai/lore/op"
)

// rawFlag is a pflag.Value that only records what the user typed. Parsing is
// left to op.ArgsDecoder so the CLI and every other surface share one set of
// parse rules and error messages; the flag still reports the field's type so
// --help reads `--limit int`, `--wait duration`.
type rawFlag struct {
	typ  op.FieldType
	vals []string
	// pos[i] is how many positional arguments the parser had seen when
	// vals[i] was set (op.ArgsDecoder.FlagPos): a repeated group gives a
	// flag typed after an item to that item (P3-TASK-1). at reads the
	// count; nil records nothing.
	pos []int
	at  func() int
}

// String implements pflag.Value; it is what --help prints as the default.
func (f *rawFlag) String() string { return strings.Join(f.vals, ",") }

// Set implements pflag.Value. A repeated scalar flag records every value and
// the decoder refuses it ("--title takes one value"), so a typo is never
// silently resolved to the last one.
func (f *rawFlag) Set(s string) error {
	f.vals = append(f.vals, s)
	if f.at != nil {
		f.pos = append(f.pos, f.at())
	}
	return nil
}

// Type implements pflag.Value: the word --help shows after the flag name.
func (f *rawFlag) Type() string { return typeWord(f.typ) }

// Help words per field type, shown after a flag name in --help.
const (
	wordString   = "string"
	wordBool     = "bool"
	wordInt      = "int"
	wordUint     = "uint"
	wordFloat    = "float"
	wordDuration = "duration"
	wordStrings  = "stringArray"
	wordMap      = "key=value"
)

// typeWord is the --help word for a field type.
func typeWord(t op.FieldType) string {
	switch t {
	case op.TypeBool:
		return wordBool
	case op.TypeInt:
		return wordInt
	case op.TypeUint:
		return wordUint
	case op.TypeFloat:
		return wordFloat
	case op.TypeDuration:
		return wordDuration
	case op.TypeStrings:
		return wordStrings
	case op.TypeStringMap:
		return wordMap
	case op.TypeString, op.TypeText, op.TypeObject, op.TypeList:
	}
	return wordString
}

// boolNoValue is what a bool flag given without "=value" means.
const boolNoValue = "true"
