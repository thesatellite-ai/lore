// Package fromfile turns the parse errors of a file a person hands lore
// (member add --from, task import, meta import, resource import) into
// refusals that name the file line, the reason and the fix for that file.
//
// Why it exists: errs.Wrap keeps the parser's error as a hidden Cause, and
// the surfaces print only Message and Hint, so a broken quote reached the
// person as "--from staff.csv: line 2" with no reason and no fix. Every
// --from reader goes through here, so the class is fixed once.
package fromfile

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Problem is one bad line (or entry) of a file: where it is, what is wrong
// and, when lore knows it, what to change on that line.
type Problem struct {
	// At is "line 8" (CSV, header counted) or "entry 3" (a JSON list).
	At string
	// Reason is what is wrong, for a person ("\"x\" is not an email address").
	Reason string
	// Fix is what to change there; empty when the reason says it.
	Fix string
	// Code is the refusal's code when the check had its own (E_ROLE_UNKNOWN);
	// empty is E_INVALID_INPUT.
	Code errs.Code
}

// Line is the label of a CSV file line (1 = the header).
func Line(n int) string { return "line " + strconv.Itoa(n) }

// Entry is the label of the n-th item of a JSON list (1-based).
func Entry(n int) string { return "entry " + strconv.Itoa(n) }

// Refuse is one refusal naming every problem of path, so a person fixes
// the whole file in one pass instead of one round-trip per bad line. what
// is the command ("member add --from"); recheck is the command that checks
// the file again without writing ("lore member add --from <path>
// --dry-run"). An empty path is input from flags: the problems are listed
// without a file. The code is the problems' own when they all share one,
// else E_INVALID_INPUT. Nil when there is no problem.
func Refuse(what, path string, ps []Problem, recheck string) *errs.Error {
	if len(ps) == 0 {
		return nil
	}
	code := cmp.Or(ps[0].Code, errs.InvalidInput)
	reasons := make([]string, 0, len(ps))
	var fixes, places []string
	for _, p := range ps {
		if cmp.Or(p.Code, errs.InvalidInput) != code {
			code = errs.InvalidInput
		}
		reason, fix := p.Reason, p.Fix
		if p.At != "" {
			reason, fix = p.At+": "+reason, p.At+": "+fix
			if !slices.Contains(places, p.At) {
				places = append(places, p.At)
			}
		}
		reasons = append(reasons, reason)
		if p.Fix != "" {
			fixes = append(fixes, fix)
		}
	}
	if path == "" {
		e := errs.New(code, what+" (nothing written): "+strings.Join(reasons, "; "))
		if len(fixes) > 0 {
			e = e.WithHint(strings.Join(fixes, "; "))
		}
		return e
	}
	n := strconv.Itoa(len(places)) + " lines need"
	if len(places) == 1 {
		n = "1 line needs"
	}
	hint := "in " + filepath.Base(path) + " fix "
	if len(fixes) > 0 {
		hint += strings.Join(fixes, "; ")
	} else {
		hint += and(places)
	}
	if recheck != "" {
		hint += "; then check it with " + recheck
	}
	return errs.New(code, what+" "+path+": "+n+" a fix, nothing written: "+strings.Join(reasons, "; ")).WithHint(hint)
}

// and is "line 8, line 9 and line 12".
func and(at []string) string {
	if len(at) == 1 {
		return at[0]
	}
	return strings.Join(at[:len(at)-1], ", ") + " and " + at[len(at)-1]
}

// CSVProblem reads a csv.ParseError as the problem of its line, with the
// reason in words and the fix; raw is the whole file, used to tell a quote
// that is never closed from text after a closing quote. ok is false for an
// error that is not a csv.ParseError.
func CSVProblem(raw []byte, err error) (Problem, bool) {
	var pe *csv.ParseError
	if !errors.As(err, &pe) {
		return Problem{}, false
	}
	start := pe.StartLine
	if start == 0 {
		start = pe.Line
	}
	at := Line(start)
	switch {
	case errors.Is(pe.Err, csv.ErrQuote) && (pe.Line > start || strings.Count(lineOf(raw, start), `"`)%2 == 1):
		return Problem{At: at, Reason: `a quote (") opens a cell and is never closed, so the rest of the file reads as that one cell`,
			Fix: `close the quote, or remove it (a cell holding a comma goes in "…")`}, true
	case errors.Is(pe.Err, csv.ErrQuote):
		return Problem{At: at, Reason: "column " + strconv.Itoa(pe.Column) + ": text after the closing quote of a quoted cell",
			Fix: `put the whole cell inside the quotes, and write a quote inside it as ""`}, true
	case errors.Is(pe.Err, csv.ErrBareQuote):
		return Problem{At: at, Reason: "column " + strconv.Itoa(pe.Column) + `: a quote (") inside a cell that is not quoted`,
			Fix: `wrap the cell in "…" and write the quote inside it as ""`}, true
	case errors.Is(pe.Err, csv.ErrFieldCount):
		return Problem{At: at, Reason: "it has a different number of cells than the header",
			Fix: "give it the header's columns (an empty cell is just ,,)"}, true
	}
	return Problem{At: at, Reason: pe.Err.Error()}, true
}

// CSV is the refusal for a CSV that does not parse (one problem); a non-CSV
// error keeps its text in the message, never as a hidden cause.
func CSV(what, path string, raw []byte, err error, recheck string) *errs.Error {
	if p, ok := CSVProblem(raw, err); ok {
		return Refuse(what, path, []Problem{p}, recheck)
	}
	return errs.Wrap(errs.InvalidInput, what+" "+path+": the CSV does not parse: "+err.Error(), err)
}

// JSON is the refusal for a JSON file that does not decode, naming the file
// line of the syntax or type error and its reason; want is the shape the
// file must have ("a JSON list of people").
func JSON(what, path string, raw []byte, err error, want, example string) *errs.Error {
	reason := err.Error()
	line := 0
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		line = lineAt(raw, se.Offset)
		reason = "the JSON breaks: " + se.Error()
	case errors.As(err, &te):
		line = lineAt(raw, te.Offset)
		reason = "found a " + te.Value
		if te.Field != "" {
			reason = "field " + strconv.Quote(te.Field) + " is a " + te.Value + ", not a " + te.Type.String()
		}
	}
	msg := what + " " + path + ": not " + want + ": " + reason
	if line > 0 {
		msg = what + " " + path + ": " + Line(line) + ": not " + want + ": " + reason
	}
	return errs.Wrap(errs.InvalidInput, msg, err).WithHint("the file must look like " + example)
}

// Unreadable is the refusal for a --from file lore cannot open, with the
// operating system's reason ("no such file or directory") in the message:
// the file is the person's input, so it is E_INVALID_INPUT, never internal.
func Unreadable(what, path string, err error) *errs.Error {
	reason := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		reason = pe.Err.Error()
	}
	return errs.Wrap(errs.InvalidInput, what+": cannot read "+path+": "+reason, err)
}

// lineOf is the text of file line n (1-based), "" past the end.
func lineOf(raw []byte, n int) string {
	ls := bytes.Split(raw, []byte("\n"))
	if n < 1 || n > len(ls) {
		return ""
	}
	return string(ls[n-1])
}

// lineAt is the 1-based line of byte offset off in raw.
func lineAt(raw []byte, off int64) int {
	if off < 0 {
		return 0
	}
	if off > int64(len(raw)) {
		off = int64(len(raw))
	}
	return bytes.Count(raw[:off], []byte("\n")) + 1
}
