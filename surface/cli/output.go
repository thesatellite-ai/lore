package cli

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/thesatellite-ai/lore/logic/listcap"
	"github.com/thesatellite-ai/lore/logic/sessionmask"
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

// shownText is text as a person reads it: session tokens masked, and every
// list of ids capped to listcap.Shown items, the rest named with where the
// full list is (listcap.Text; jsonHint). --json output never comes here.
func shownText(text, where string) string { return listcap.Text(masked(text), where) }

// jsonHint is where a capped list is printed whole: "`<cmd> --json` lists
// all" (cmd is the command path, e.g. "lore doctor"; "" names --json).
func jsonHint(cmd string) string {
	if cmd == "" {
		return "--json lists all"
	}
	return "`" + cmd + " --" + FlagJSON + "` lists all"
}

// masked is text with every session-shaped token masked (PASS3-A-7,
// supersedes FB-SESS-2): every byte the surface prints goes through it, so
// a tool session id never reaches output, whether or not lore.db (where
// the known ids live) can be read: the shape and the session variables of
// this process need no lore.db (fail closed).
func masked(text string) string {
	return sessionmask.Mask(text, sessionmask.EnvIDs(os.Getenv))
}

// printJSON writes the result as one JSON value and a newline (--json),
// indented with two spaces as v0.1.17's printJSON did (CLI_REFERENCE 1.4,
// OWNERQ-16). Every --json output is indented, the v0.1.17 envelopes
// (schema_version, kind, data) and the plain ones (lore show, config list,
// trash list) alike, so a reader never meets two shapes from one binary;
// the compat nouns' own re-indent (wire/host WriteIndented) leaves it as
// is. REST answers do not come through here and stay compact.
func printJSON(w io.Writer, r op.Result) error {
	b, err := r.JSON()
	if err != nil {
		return err
	}
	var ind bytes.Buffer
	if json.Indent(&ind, b, "", indentStep) == nil {
		b = ind.Bytes()
	}
	_, err = io.WriteString(w, masked(string(b))+"\n")
	return err
}

// printHuman writes the result as `name: value` lines; a list field becomes
// a table under its name, an object field an indented block. Optional
// (omitempty) fields holding their zero value are skipped, as in the JSON.
// pal colours the success and warning lines (colour.go; off = plain).
func printHuman(w io.Writer, r op.Result, pal Palette, cmd string) error {
	v := r.Value()
	if !v.IsValid() {
		return nil
	}
	// An output with its own human form (op.Texter) prints exactly that,
	// with the Runner's edits (a purged delete's line, P3-DEL-1).
	if t, ok := r.Text(); ok {
		_, err := io.WriteString(w, pal.Lines(shownText(t, jsonHint(cmd))))
		return err
	}
	var b strings.Builder
	if err := writeObject(&b, v, r.Fields(), ""); err != nil {
		return err
	}
	_, err := io.WriteString(w, pal.Lines(shownText(b.String(), jsonHint(cmd))))
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
// with --json the spec 9.4 object on stdout, else the human line on stderr,
// its "ERROR:" and hint coloured by pal (the stderr palette; off = plain).
func printError(stdout, stderr io.Writer, err error, asJSON bool, pal Palette, cmd string) int {
	env := errs.EnvelopeOf(err)
	if asJSON {
		// Envelope is a fixed struct of strings and ints: Marshal cannot
		// fail on it, and a failing writer has nowhere left to report to.
		// Indented like v0.1.17's printJSON, so every --json error has one
		// shape whichever noun or host command printed it (E-GAPS-1 S3;
		// the compat nouns re-indent their output, which leaves it as is).
		b, mErr := json.MarshalIndent(env, "", indentStep)
		if mErr == nil {
			_, _ = io.WriteString(stdout, masked(string(b))+"\n")
		}
		return env.Error.Exit
	}
	where := jsonHint(cmd)
	line := pal.Error(strings.TrimSuffix(errorPrefix, " ")) + " [" + string(env.Error.Code) + "] " + shownText(env.Error.Message, where) + "\n"
	if env.Error.Hint != "" {
		line += hintPrefix + pal.Hint(shownText(env.Error.Hint, where)) + "\n"
	}
	_, _ = io.WriteString(stderr, line)
	return env.Error.Exit
}
