package cli

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Output formatting constants.
const (
	// errorPrefix starts the human error line (CLI_REFERENCE 1.4, the
	// v0.1.17 format kept as is).
	errorPrefix = "ERROR: "
	// hintPrefix starts the line under an error that tells a person what to
	// do next.
	hintPrefix = "  hint: "
	// indentStep indents a nested object in human output.
	indentStep = "  "
	// listSep joins the items of a []string or map value on one line.
	listSep = ", "
	// tabPadding is the gap between table columns.
	tabPadding = 2
	// floatBits is the precision human output prints floats at.
	floatBits = 64
)

// printJSON writes the result as one JSON object and a newline (--json).
func printJSON(w io.Writer, r op.Result) error {
	b, err := r.JSON()
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// printHuman writes the result as `name: value` lines; a list field becomes
// a table under its name, an object field an indented block. Optional
// (omitempty) fields holding their zero value are skipped, as in the JSON.
func printHuman(w io.Writer, r op.Result) error {
	v := r.Value()
	if !v.IsValid() {
		return nil
	}
	var b strings.Builder
	if err := writeObject(&b, v, r.Fields(), ""); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeObject writes the fields of struct value v at an indent.
func writeObject(b *strings.Builder, v reflect.Value, fields []op.FieldInfo, indent string) error {
	for _, f := range fields {
		fv, ok := jsonField(v, f.Name)
		if !ok || (!f.Required && fv.IsZero()) {
			continue
		}
		switch f.Type {
		case op.TypeObject:
			fmt.Fprintf(b, "%s%s:\n", indent, f.Name)
			if err := writeObject(b, fv, f.Fields, indent+indentStep); err != nil {
				return err
			}
		case op.TypeList:
			fmt.Fprintf(b, "%s%s:\n", indent, f.Name)
			if err := writeTable(b, fv, f.Fields, indent+indentStep); err != nil {
				return err
			}
		case op.TypeString, op.TypeBool, op.TypeInt, op.TypeUint, op.TypeFloat, op.TypeDuration,
			op.TypeStrings, op.TypeStringMap, op.TypeText:
			fmt.Fprintf(b, "%s%s: %s\n", indent, f.Name, scalarText(fv, f.Type))
		}
	}
	return nil
}

// writeTable writes a list of structs as an aligned table, one column per
// scalar child field; a nested object or list cell prints as compact JSON.
func writeTable(b *strings.Builder, list reflect.Value, fields []op.FieldInfo, indent string) error {
	tw := tabwriter.NewWriter(b, 0, 0, tabPadding, ' ', 0)
	head := make([]string, len(fields))
	for i, f := range fields {
		head[i] = strings.ToUpper(f.Name)
	}
	if _, err := fmt.Fprintln(tw, indent+strings.Join(head, "\t")); err != nil {
		return err
	}
	for i := range list.Len() {
		item := list.Index(i)
		cells := make([]string, len(fields))
		for j, f := range fields {
			fv, ok := jsonField(item, f.Name)
			if !ok {
				continue
			}
			cells[j] = cellText(fv, f)
		}
		if _, err := fmt.Fprintln(tw, indent+strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// cellText is one table cell.
func cellText(fv reflect.Value, f op.FieldInfo) string {
	switch f.Type {
	case op.TypeObject, op.TypeList:
		// json.Marshal takes `any` (stdlib signature); the concrete value
		// goes straight in and only bytes come back.
		b, err := json.Marshal(fv.Interface())
		if err != nil {
			return "?"
		}
		return string(b)
	case op.TypeString, op.TypeBool, op.TypeInt, op.TypeUint, op.TypeFloat, op.TypeDuration,
		op.TypeStrings, op.TypeStringMap, op.TypeText:
	}
	return scalarText(fv, f.Type)
}

// scalarText formats one non-nested value for people.
func scalarText(fv reflect.Value, t op.FieldType) string {
	switch t {
	case op.TypeString:
		return fv.String()
	case op.TypeBool:
		return strconv.FormatBool(fv.Bool())
	case op.TypeInt:
		return strconv.FormatInt(fv.Int(), 10)
	case op.TypeUint:
		return strconv.FormatUint(fv.Uint(), 10)
	case op.TypeFloat:
		return strconv.FormatFloat(fv.Float(), 'g', -1, floatBits)
	case op.TypeDuration:
		return time.Duration(fv.Int()).String()
	case op.TypeStrings:
		items := make([]string, fv.Len())
		for i := range items {
			items[i] = fv.Index(i).String()
		}
		return strings.Join(items, listSep)
	case op.TypeStringMap:
		var kv []string
		iter := fv.MapRange()
		for iter.Next() {
			kv = append(kv, iter.Key().String()+"="+iter.Value().String())
		}
		slices.Sort(kv)
		return strings.Join(kv, listSep)
	case op.TypeText:
		// Interface returns `any` (stdlib signature); op classifies a field
		// as TypeText only when the type implements encoding.TextMarshaler,
		// so the assertion holds and nothing untyped escapes this line.
		if m, ok := fv.Interface().(encoding.TextMarshaler); ok {
			if b, err := m.MarshalText(); err == nil {
				return string(b)
			}
		}
		return "?"
	case op.TypeObject, op.TypeList:
	}
	return ""
}

// jsonField finds the field of struct v whose json name is name.
//
// Why: op.FieldInfo keeps its struct index unexported, so a printer that
// walks Result.Value finds each field again by its json tag. The tag is the
// same one op.Define read, so the two can never disagree.
func jsonField(v reflect.Value, name string) (reflect.Value, bool) {
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	t := v.Type()
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag == name && t.Field(i).IsExported() {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// printError writes err in the chosen format and returns its exit status:
// with --json the spec 9.4 object on stdout, else the human line on stderr.
func printError(stdout, stderr io.Writer, err error, asJSON bool) int {
	env := errs.EnvelopeOf(err)
	if asJSON {
		// Envelope is a fixed struct of strings and ints: Marshal cannot
		// fail on it, and a failing writer has nowhere left to report to.
		b, mErr := json.Marshal(env)
		if mErr == nil {
			_, _ = stdout.Write(append(b, '\n'))
		}
		return env.Error.Exit
	}
	line := errorPrefix + "[" + string(env.Error.Code) + "] " + env.Error.Message + "\n"
	if env.Error.Hint != "" {
		line += hintPrefix + env.Error.Hint + "\n"
	}
	_, _ = io.WriteString(stderr, line)
	return env.Error.Exit
}
