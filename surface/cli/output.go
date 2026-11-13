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
	// pairSep joins the "name: value" pairs of an object in a table cell
	// (objectCell); unlike listSep it cannot be read as one list's items.
	pairSep = "; "
	// maxCell is the widest a table cell prints (cutCell); wide enough for
	// an id, a path or a url, narrow enough that a row of prose fits a
	// wide terminal.
	maxCell = 80
	// itemSep joins the objects of a list in a table cell.
	itemSep = " | "
	// humanTime is how a time value prints for people: to the second, the
	// zone kept (RFC 3339 without the nanoseconds MarshalText gives); --json
	// keeps the full value.
	humanTime = time.RFC3339
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
		if !ok || (!f.Required && fv.IsZero()) || emptyList(fv, f.Type) {
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
// child field; a nested object cell prints as readable text (cellText). A
// list whose items hold a list of objects prints one block per item
// instead (writeBlocks): such a cell is a table of its own, and on one
// table line it ran to hundreds of characters (B1-3, import-scan FIELDS).
func writeTable(b *strings.Builder, list reflect.Value, fields []op.FieldInfo, indent string) error {
	if slices.ContainsFunc(fields, func(f op.FieldInfo) bool { return f.Type == op.TypeList }) {
		return writeBlocks(b, list, fields, indent)
	}
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
			if !ok || omittedZero(fv, f) {
				continue
			}
			cells[j] = cutCell(cellText(fv, f))
		}
		if _, err := fmt.Fprintln(tw, indent+strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// omittedZero reports whether a table cell is an optional field at its
// zero value, which --json leaves out: a blank cell, never "0" (W-8: the
// LINE column of import-scan read 0 for values with no line). A bool is
// never optional here (op marks every bool not required), so "false"
// still prints.
func omittedZero(fv reflect.Value, f op.FieldInfo) bool {
	return !f.Required && f.Type != op.TypeBool && fv.IsZero()
}

// emptyList reports whether fv is a list or map field with no items. Why:
// a required one printed "wrote: " with nothing after it (B2-17), and an
// empty list of records printed its table header with no row (a setup
// step with no check that applies, W-6; B1-26's rule); --json keeps the
// empty list.
func emptyList(fv reflect.Value, t op.FieldType) bool {
	return (t == op.TypeStrings || t == op.TypeStringMap || t == op.TypeList) && fv.Kind() != reflect.Invalid && fv.Len() == 0
}

// cutCell is a table cell cut to maxCell characters, the cut marked with
// an ellipsis. Why: one long cell (a question's help text, its choices)
// made every line of `lore setup plan` hundreds of characters wide
// (B2-17); --json keeps the whole value.
func cutCell(text string) string {
	r := []rune(text)
	if len(r) <= maxCell {
		return text
	}
	return string(r[:maxCell-1]) + "…"
}

// writeBlocks writes a list of structs as one "- " block per item, each
// item's fields as writeObject prints them (a nested list becomes a table
// under its name), for a list too deep for one table line.
func writeBlocks(b *strings.Builder, list reflect.Value, fields []op.FieldInfo, indent string) error {
	for i := range list.Len() {
		var item strings.Builder
		if err := writeObject(&item, list.Index(i), fields, indent+indentStep); err != nil {
			return err
		}
		text := item.String()
		if text == "" {
			fmt.Fprintf(b, "%s-\n", indent)
			continue
		}
		// the first line's indent becomes the item marker
		b.WriteString(indent + "- " + strings.TrimPrefix(text, indent+indentStep))
	}
	return nil
}

// cellText is one table cell.
func cellText(fv reflect.Value, f op.FieldInfo) string {
	switch f.Type {
	case op.TypeObject:
		return objectCell(fv, f.Fields)
	case op.TypeList:
		items := make([]string, 0, fv.Len())
		for i := range fv.Len() {
			if t := objectCell(fv.Index(i), f.Fields); t != "" {
				items = append(items, t)
			}
		}
		return strings.Join(items, itemSep)
	case op.TypeString, op.TypeBool, op.TypeInt, op.TypeUint, op.TypeFloat, op.TypeDuration,
		op.TypeStrings, op.TypeStringMap, op.TypeText:
	}
	return scalarText(fv, f.Type)
}

// objectCell is a nested object on one table line, as a person reads it:
// its set fields as "name: value", joined by pairSep; a zero field is left
// out, so an object with nothing set is a blank cell. A lone set field is
// its bare value.
//
// Why: the cell was the object's compact JSON, so every check of `lore
// setup apply` showed {"text":"","command":null,"runnable":false,
// "outward":false} in its FIX column (B1-3). --json keeps the JSON.
func objectCell(v reflect.Value, fields []op.FieldInfo) string {
	var pairs, vals []string
	for _, f := range fields {
		fv, ok := jsonField(v, f.Name)
		if !ok || fv.IsZero() {
			continue
		}
		t := cellText(fv, f)
		if t == "" {
			continue
		}
		pairs = append(pairs, f.Name+": "+t)
		vals = append(vals, t)
	}
	if len(vals) == 1 {
		return vals[0]
	}
	return strings.Join(pairs, pairSep)
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
		// A time to the nanosecond (2026-11-12T19:39:43.342161897Z, the
		// CHECKED_AT column of B1-3) reads as noise: print it to the second.
		if t, ok := fv.Interface().(time.Time); ok {
			return t.Format(humanTime)
		}
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
