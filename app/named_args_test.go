package app

// U-4 (owner decision 2026-11-13): named args everywhere. Every command's
// canonical form is named flags (`lore task add --title "x"`, `lore task
// show --id tsk_…`); a positional value stays a quiet alias that nothing
// lore shows may teach. TestDocsMatchCLI holds the docs and the skill to
// it (checkInvocation); this file holds what the binary itself prints:
// every --help Examples line and every `lore …` command written in a Go
// string literal (hints, "run this" lines, error messages).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/surface/cli"
)

// TestHelpExamplesNamed: no generated command's --help Examples line gives
// a value by position, or a flag's value with = (--severity=must): the
// named form is --flag value.
func TestHelpExamplesNamed(t *testing.T) {
	root := driftRoot(t)
	checked := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			walk(s)
			if !cli.Generated(s) || s.Example == "" {
				continue
			}
			for _, line := range strings.Split(s.Example, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "#") || line == "" {
					continue
				}
				for _, loc := range invocationStart.FindAllStringIndex(line, -1) {
					code := line[loc[0]+strings.Index(line[loc[0]:], binaryName):]
					if end := commandEnd.FindStringIndex(code[len(binaryName):]); end != nil {
						code = code[:len(binaryName)+end[0]]
					}
					checked++
					if p := checkInvocation(root, strings.TrimSpace(code)); p != "" {
						t.Errorf("%s --help example: %s\n    %s", s.CommandPath(), p, code)
					}
					if f := equalsValue(code); f != "" {
						t.Errorf("%s --help example gives %s with =: write it %s\n    %s", s.CommandPath(), f, spaced(f), code)
					}
				}
			}
		}
	}
	walk(root)
	if checked < minDocInvocations {
		t.Fatalf("only %d examples found; the walk is broken", checked)
	}
}

// namedArgsSourceDirs are the source trees whose strings reach a person.
var namedArgsSourceDirs = []string{"app", "cmd", "engine", "ops", "surface", "wire", "adapters", "logic", "store", "op", "internal"}

// TestPrintedCommandsNamed: every `lore …` command spelled out in a Go
// string literal of the binary's source (a hint, a "run this" line, an
// error message) gives its values by flag, each as --flag value (not
// --flag=value). A literal cut before its value
// ("lore task show " + id) has nothing to check and passes; write such a
// line with the flag in the literal ("lore task show --id " + id).
func TestPrintedCommandsNamed(t *testing.T) {
	root := driftRoot(t)
	checked := 0
	for _, dir := range namedArgsSourceDirs {
		err := filepath.WalkDir(filepath.Join(docsRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					return true
				}
				for _, line := range strings.Split(s, "\n") {
					quoted := strings.Contains(line, "`")
					for _, span := range commandSpans(line) {
						checked++
						if _, ok := printedAllowed[span]; ok {
							continue
						}
						if f := equalsValue(span); f != "" {
							rel, _ := filepath.Rel(docsRoot, path)
							t.Errorf("%s:%d: gives %s with =: write it %s\n    %s", rel, fset.Position(lit.Pos()).Line, f, spaced(f), span)
						}
						if p := checkInvocation(root, span); p != "" && strings.Contains(p, "positional value") && (quoted || valueLike(p)) {
							rel, _ := filepath.Rel(docsRoot, path)
							t.Errorf("%s:%d: %s\n    %s", rel, fset.Position(lit.Pos()).Line, p, span)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < minDocInvocations {
		t.Fatalf("only %d commands found in the source; the scan is broken", checked)
	}
}

// printedAllowed are commands in the source that are wrong on purpose,
// keyed by the exact command text, each with its reason.
var printedAllowed = map[string]string{
	`lore rule add --severity=must "<X>" --body="<Y>"`: "ops/skill/compile.go: the FORBIDDEN PATTERN an agent must never emit",
}

// commandSpans are the `lore …` commands in one line of a Go string: the
// backtick spans when it has any (a hint quoting a command), else the
// command starting the line.
func commandSpans(line string) []string {
	var parts []string
	if strings.Contains(line, "`") {
		for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
			parts = append(parts, m[1])
		}
	} else {
		parts = []string{line}
	}
	var out []string
	for _, s := range parts {
		s = foldSubstitutions(s)
		for _, loc := range printedStart.FindAllStringIndex(s, -1) {
			code := s[loc[0]+strings.Index(s[loc[0]:], binaryName):]
			if end := commandEnd.FindStringIndex(code[len(binaryName):]); end != nil {
				code = code[:len(binaryName)+end[0]]
			}
			out = append(out, strings.TrimSpace(proseEnd(code)))
		}
	}
	return out
}

// flagEquals is a flag given its value with "=" (--severity=must,
// --body="x"). The named form is a space: --severity must.
var flagEquals = regexp.MustCompile(`--[a-z][a-z0-9-]*=("[^"]*"|'[^']*'|\S*)`)

// equalsValue is the first --flag=value of a command ("" when none). A
// switch spelled --x=true / --x=false and an empty --x= (or --x="", an
// example of the empty-value error) are not values and pass.
func equalsValue(code string) string {
	for _, m := range flagEquals.FindAllString(code, -1) {
		_, v, _ := strings.Cut(m, "=")
		switch v {
		case "", `""`, "''", "true", "false":
			continue
		}
		return m
	}
	return ""
}

// spaced is --flag=value written the named way, --flag value.
func spaced(f string) string { return strings.Replace(f, "=", " ", 1) }

// printedStart finds a command anywhere in a printed line, not only at its
// start: a hint names it mid-sentence ("…; if it is on the wrong person,
// lore actor contact remove --id <acn_id> there"). The docs scan keeps
// invocationStart (prose there names lore all the time); checkInvocation
// still only reports a real command given a value by position.
var printedStart = regexp.MustCompile(`(?:^|[\s(,:;"'])lore\s`)

// proseEnd cuts a printed command where its sentence goes on: at a ";",
// "," or "." followed by a space or the end (outside quotes), and at a
// double quote that closes the string the command was quoted in ("run
// \"lore sync trash\" to …"). A literal cut at a flag's "--" (the name is
// joined on: "… --" + relation) ends there too.
func proseEnd(code string) string {
	code = strings.TrimSuffix(code, " --")
	var quote byte
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' && i > 0 && code[i-1] != ' ' && code[i-1] != '=':
			return code[:i] // closes a quote opened before the command
		case c == '"' || c == '\'':
			if strings.IndexByte(code[i+1:], c) < 0 {
				return code[:i] // never closed here: the line's own quote
			}
			quote = c
		case (c == ';' || c == ',' || c == '.') && (i+1 == len(code) || code[i+1] == ' '):
			return code[:i]
		}
	}
	return code
}

// proseValue is a positional the scan read from a plain sentence, not a
// value: an English word, maybe with its punctuation ("lore trash restore
// brings it back", "lore fix --all: …").
var proseValue = regexp.MustCompile(`^(--[a-z-]+:|[a-z]+[,.;:)]?)$`)

// valueLike reports whether the positional a checkInvocation problem names
// looks like a value a person would type (an id, a placeholder, a quoted
// text, a path, a key=value): in a plain sentence only those are findings.
// A backtick span is a command in full and is always checked.
func valueLike(problem string) bool {
	const marker = "given the positional value "
	i := strings.Index(problem, marker)
	if i < 0 {
		return false
	}
	v, _, _ := strings.Cut(problem[i+len(marker):], ": write it with its flag")
	return !proseValue.MatchString(v) && v != "%q"
}
