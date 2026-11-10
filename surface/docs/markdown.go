package docs

import (
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Markdown pieces.
const (
	cellSep   = " | "
	rowStart  = "| "
	rowEnd    = " |"
	cliPrefix = "lore "
	tick      = "`"
)

// CommandsMarkdown returns the CLI_REFERENCE command-list table: one row per
// non-hidden operation, sorted by name, with its usage line, flags, REST
// route, permission and error codes.
//
// Invariant: deterministic (registry order is sorted by name); every cell is
// derived from the registry, so the table cannot drift from the binary.
func CommandsMarkdown(reg *op.Registry) string {
	var b strings.Builder
	b.WriteString("| Command | Summary | Flags | REST | Permission | Errors |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, e := range reg.Entries() {
		s := e.Spec()
		if s.Hidden {
			continue
		}
		rest := ""
		if rt, ok := op.RouteOf(s, e.InputFields()); ok {
			rest = code(rt.Method + " " + rt.Path)
		}
		var flags, codes []string
		for _, f := range e.InputFields() {
			if f.Flag != "" {
				flag := code("--" + f.Flag)
				for _, a := range f.Aliases {
					flag += " (or " + code("--"+a) + ")"
				}
				flags = append(flags, flag)
			}
		}
		for _, c := range s.Errors {
			codes = append(codes, code(string(c)))
		}
		row := []string{code(usageLine(e)), cell(s.Summary), strings.Join(flags, " "), rest, string(s.Permission), strings.Join(codes, " ")}
		b.WriteString(rowStart + strings.Join(row, cellSep) + rowEnd + "\n")
	}
	return b.String()
}

// ErrorsMarkdown returns the error table written into CLI_REFERENCE section
// 5: every registered code with its exit status, class and HTTP status,
// all read from model/errs, sorted by code.
func ErrorsMarkdown() string {
	var b strings.Builder
	b.WriteString("| Code | Exit | Class | HTTP | Meaning |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, d := range errs.All() {
		row := []string{code(string(d.Code)), strconv.Itoa(d.Exit), code(string(d.Class)), strconv.Itoa(d.HTTP), cell(d.Description)}
		b.WriteString(rowStart + strings.Join(row, cellSep) + rowEnd + "\n")
	}
	return b.String()
}

// usageLine is `lore <words> <arg> [arg]`.
func usageLine(e op.Entry) string {
	words := op.CLIPath(e.Spec().Name)
	args := map[int]op.FieldInfo{}
	for _, f := range e.InputFields() {
		if f.Arg != op.NoArg {
			args[f.Arg] = f
		}
	}
	for i := range len(args) {
		f := args[i]
		if f.Required {
			words = append(words, "<"+f.Name+">")
		} else {
			words = append(words, "["+f.Name+"]")
		}
	}
	return cliPrefix + strings.Join(words, " ")
}

// code wraps s in backticks.
func code(s string) string { return tick + s + tick }

// cell escapes a table cell: a pipe would split the row, a newline would end
// it.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}
