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
// a value by position.
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
// error message) gives its values by flag. A literal cut before its value
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
		for _, loc := range invocationStart.FindAllStringIndex(s, -1) {
			code := s[loc[0]+strings.Index(s[loc[0]:], binaryName):]
			if end := commandEnd.FindStringIndex(code[len(binaryName):]); end != nil {
				code = code[:len(binaryName)+end[0]]
			}
			out = append(out, strings.TrimSpace(code))
		}
	}
	return out
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
