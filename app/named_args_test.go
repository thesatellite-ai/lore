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
	"slices"
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
			if !cli.Named(s) || s.Example == "" {
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
// --flag=value). A literal cut before its value and joined to a variable
// ("lore task show " + id) is checked with the joined value standing in as
// <value> (H-11: "lore edit " + id printed `lore edit mem_…` with the id by
// position and the scan passed it); write such a line with the flag in the
// literal ("lore task show --id " + id).
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
			rel, _ := filepath.Rel(docsRoot, path)
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					s, ok := stringLit(x)
					if !ok {
						return true
					}
					for _, line := range strings.Split(s, "\n") {
						n, problems := printedProblems(root, line)
						checked += n
						for _, p := range problems {
							t.Errorf("%s:%d: %s", rel, fset.Position(x.Pos()).Line, p)
						}
					}
				case *ast.BinaryExpr:
					// "lore edit " + id: the literal's last line with the
					// joined value standing in. Only what the join adds is
					// reported; the literal alone is checked above.
					lit, ok := joinedLit(x)
					if !ok {
						return true
					}
					lines := strings.Split(lit, "\n")
					last := lines[len(lines)-1]
					if !strings.HasSuffix(last, " ") || joinsVerb(root, last) {
						return true // "--"+name, "--claim"+s: part of a token; "lore sync "+verb: a subcommand
					}
					_, plain := printedProblems(root, last)
					seen := map[string]bool{}
					for _, p := range plain {
						seen[p] = true
					}
					joined := last + joinedValue
					if strings.Count(last, "`")%2 == 1 {
						joined += "`" // the literal opened a backtick span the value sits in
					}
					_, added := printedProblems(root, joined)
					for _, p := range added {
						if !seen[p] {
							t.Errorf("%s:%d: %s (the value joined on with +)", rel, fset.Position(x.Pos()).Line, p)
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

// joinedValue stands in for a value joined onto a literal with + ("lore
// edit " + id): a placeholder, so checkInvocation names it when it lands
// as a positional.
const joinedValue = "<value>"

// stringLit is the text of a Go string literal (false for any other
// literal, or one that does not unquote).
func stringLit(lit *ast.BasicLit) (string, bool) {
	if lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// joinedLit is the literal a non-literal value is joined onto in a + chain:
// the left operand's last literal ("lore edit " in "lore edit " + id, or in
// "x " + y + " lore edit " + id, which Go groups to the left). False when
// the right operand is itself a literal (both are scanned on their own) or
// the left side does not end in a literal.
func joinedLit(b *ast.BinaryExpr) (string, bool) {
	if b.Op != token.ADD {
		return "", false
	}
	if _, isLit := b.Y.(*ast.BasicLit); isLit {
		return "", false
	}
	left := b.X
	if inner, ok := left.(*ast.BinaryExpr); ok && inner.Op == token.ADD {
		left = inner.Y
	}
	lit, ok := left.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	return stringLit(lit)
}

// joinsVerb reports whether the last command of a literal stops at a
// command group ("lore sync " + verb): the joined value is a subcommand
// name, not a value given by position.
func joinsVerb(root *cobra.Command, line string) bool {
	spans := commandSpans(line)
	if len(spans) == 0 {
		return false
	}
	toks := docTokens(spans[len(spans)-1].code)[1:] // drop "lore"
	cmd := root
	for _, tok := range toks {
		next := findSub(cmd, tok)
		if next == nil {
			return false
		}
		cmd = next
	}
	return cmd.HasSubCommands()
}

// printedProblems checks the `lore …` commands of one printed line: how
// many it found, and each problem with the command it is about.
func printedProblems(root *cobra.Command, line string) (int, []string) {
	spans := commandSpans(line)
	var out []string
	for _, sp := range spans {
		span := sp.code
		if _, ok := printedAllowed[span]; ok {
			continue
		}
		if f := equalsValue(span); f != "" {
			out = append(out, "gives "+f+" with =: write it "+spaced(f)+"\n    "+span)
		}
		if p := checkInvocation(root, span); p != "" && strings.Contains(p, "positional value") && (sp.quoted || valueLike(p, span)) {
			out = append(out, p+"\n    "+span)
		}
	}
	return len(spans), out
}

// printedAllowed are commands in the source that are wrong on purpose,
// keyed by the exact command text, each with its reason.
var printedAllowed = map[string]string{
	`lore rule add --severity=must "<X>" --body="<Y>"`: "ops/skill/compile.go: the FORBIDDEN PATTERN an agent must never emit",
}

// printedSpan is one `lore …` command found in a Go string; quoted when
// it sat in backticks (a command in full, always checked).
type printedSpan struct {
	code   string
	quoted bool
}

// commandSpans are the `lore …` commands in one line of a Go string, in
// line order: each backtick span (a hint quoting a command) and every
// command in the prose around them. H-15 (D-760): the prose outside the
// backticks was skipped once a line held any, so "e.g. lore store adopt
// team/client_north (`lore store folder list` …)" taught a positional
// unseen. A lone backtick (a span cut across literals) ends the command
// there.
func commandSpans(line string) []printedSpan {
	type part struct {
		text   string
		quoted bool
	}
	var parts []part
	prose := func(text string) { parts = append(parts, part{strings.ReplaceAll(text, "`", " ; "), false}) }
	at := 0
	for _, m := range inlineCode.FindAllStringSubmatchIndex(line, -1) {
		prose(line[at:m[0]])
		parts = append(parts, part{line[m[2]:m[3]], true})
		at = m[1]
	}
	prose(line[at:])
	var out []printedSpan
	for _, p := range parts {
		s := foldSubstitutions(p.text)
		for _, loc := range printedStart.FindAllStringIndex(s, -1) {
			code := s[loc[0]+strings.Index(s[loc[0]:], binaryName):]
			if end := commandEnd.FindStringIndex(code[len(binaryName):]); end != nil {
				code = code[:len(binaryName)+end[0]]
			}
			out = append(out, printedSpan{strings.TrimSpace(proseEnd(code)), p.quoted})
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
// "," or "." followed by a space or the end, at a " (" that opens an aside
// ("lore move --id <id> --to me --yes (lore memory list … lists them)",
// G-10: without the cut the span read as one command no parser accepts,
// and its positional <id> went unreported), outside quotes, and at a
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
		case c == '(' && i > 0 && code[i-1] == ' ':
			return strings.TrimSpace(code[:i])
		}
	}
	return code
}

// proseValue is a positional the scan read from a plain sentence, not a
// value: an English word, maybe with its punctuation ("lore trash restore
// brings it back", "lore fix --all: …").
var proseValue = regexp.MustCompile(`^(--[a-z-]+:|[a-z]+[,.;:)]?)$`)

// valueLike reports whether a positional in span looks like a value a
// person would type (an id, a placeholder, a quoted text, a path, a
// key=value): in a plain sentence only those are findings. It looks at
// the positional the problem names, then for a key=value among the words
// after it up to the first flag. H-15 (D-760): "lore store set me
// push=batch …" was read as prose because only its first positional
// ("me", an English word) was looked at. A backtick span is a command in
// full and is always checked.
func valueLike(problem, span string) bool {
	const marker = "given the positional value "
	i := strings.Index(problem, marker)
	if i < 0 {
		return false
	}
	v, _, _ := strings.Cut(problem[i+len(marker):], ": write it with its flag")
	if !proseValue.MatchString(v) && v != "%q" {
		return true
	}
	toks := docTokens(span)
	at := slices.Index(toks, v)
	if at < 0 {
		return false
	}
	for _, w := range toks[at+1:] {
		if strings.HasPrefix(w, "--") {
			break
		}
		if strings.Contains(w, "=") {
			return true
		}
	}
	return false
}
