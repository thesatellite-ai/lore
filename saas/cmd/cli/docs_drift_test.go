package main

// Docs-vs-CLI drift gate.
//
// Why it exists: the README and the agent skill (skills/*.md) are what people
// and agents copy commands from, and they had drifted — flags that never
// existed (`--name` on commands that take `--title`), JSON output scripts
// piped into jq that the command did not produce, a required `--tasklist`
// missing from every `task add` example. Each looked fine to a reader and
// failed on the first run. This test resolves every `lore …` invocation in
// the docs against the real command tree (the same cobra commands the binary
// runs) and fails on an unknown command, an unknown flag, or a missing
// required flag.
//
// Only code is checked — fenced blocks and inline `backtick` spans — never
// prose, so "lore takes the opposite bet" is not a command.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// docsRoot is the repository root, relative to this package.
const docsRoot = "../../.."

// driftDocGlobs are the documents people and agents copy commands from.
var driftDocGlobs = []string{
	"README.md",
	"CONTRIBUTING.md",
	"LORE_SYNC_SPEC.md",
	"docs/*.md",
	"skills/*.md",
	"skills/examples/*.md",
	"tests/README.md",
}

// driftAllowed lists invocations that are wrong on purpose, keyed by the
// exact code text: examples of what NOT to run, or a table row documenting
// that a command does not exist. Each needs its reason.
var driftAllowed = map[string]string{
	"lore task block <T-N>": "SKILL.md table row documenting that this verb does not exist",
	"lore add":              "capture-discipline rules: shorthand for any `lore <entity> add` (\"NO `lore add`\")",
}

// invocationStart finds `lore` where a shell command can begin: at the start
// of the line (after an optional `$ ` prompt), after `$(`, a pipe, `;`, `&&`,
// `||`, or a YAML `run:` key. A mid-sentence "lore" (a YAML name, a
// comment) is not a command.
var invocationStart = regexp.MustCompile(`(?:^\s*(?:\$\s+)?|\$\(\s*|[|;&]\s*|run:\s*)lore\s`)

// inlineCode matches `…` spans on a prose line.
var inlineCode = regexp.MustCompile("`([^`]+)`")

// commandEnd ends an invocation: shell control operators, comments, and `\|`
// (a pipe escaped inside a markdown table cell).
var commandEnd = regexp.MustCompile(`\s(?:\\?\||&&|;|#|>|2>|<<)\s?|\)|$`)

// driftFinding is one problem in one document.
type driftFinding struct {
	file, code, problem string
	line                int
}

func TestDocsMatchCLI(t *testing.T) {
	var files []string
	for _, g := range driftDocGlobs {
		m, err := filepath.Glob(filepath.Join(docsRoot, g))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < len(driftDocGlobs) {
		t.Fatalf("expected the docs set to exist, found %v", files)
	}
	var findings []driftFinding
	checked := 0
	for _, f := range files {
		for _, inv := range docInvocations(t, f) {
			if _, ok := driftAllowed[inv.code]; ok {
				continue
			}
			checked++
			if problem := checkInvocation(inv.code); problem != "" {
				rel, _ := filepath.Rel(docsRoot, f)
				findings = append(findings, driftFinding{file: rel, line: inv.line, code: inv.code, problem: problem})
			}
		}
	}
	if checked < 200 {
		t.Fatalf("only %d invocations found; the extractor is broken", checked)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		return findings[i].line < findings[j].line
	})
	for _, d := range findings {
		t.Errorf("%s:%d: %s\n    %s", d.file, d.line, d.problem, d.code)
	}
}

// docInvocation is one `lore …` command found in a document.
type docInvocation struct {
	code string
	line int
}

// docInvocations extracts every lore invocation from code in a markdown file.
func docInvocations(t *testing.T, path string) []docInvocation {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read-only
	var out []docInvocation
	inFence := false
	n, start := 0, 0
	pending := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			pending = ""
			continue
		}
		// A trailing backslash continues the command on the next line.
		if inFence && strings.HasSuffix(strings.TrimRight(line, " "), "\\") {
			if pending == "" {
				start = n
			}
			pending += strings.TrimSuffix(strings.TrimRight(line, " "), "\\") + " "
			continue
		}
		lineNo := n
		if pending != "" {
			line, lineNo, pending = pending+strings.TrimSpace(line), start, ""
		}
		var spans []string
		if inFence {
			spans = []string{line}
		} else {
			for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
				spans = append(spans, m[1])
			}
		}
		for _, s := range spans {
			for _, loc := range invocationStart.FindAllStringIndex(s, -1) {
				start := loc[0] + strings.Index(s[loc[0]:], "lore")
				rest := s[start:]
				if end := commandEnd.FindStringIndex(rest[len("lore"):]); end != nil {
					rest = rest[:len("lore")+end[0]]
				}
				out = append(out, docInvocation{code: strings.TrimSpace(rest), line: lineNo})
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// checkInvocation resolves code against the command tree and returns a
// problem description, or "" when it is valid.
func checkInvocation(code string) string {
	toks := docTokens(code)[1:] // drop "lore"
	if len(toks) > 0 && (strings.ContainsAny(toks[0][:1], "$<") || toks[0] == "..." || toks[0] == "…") {
		return "" // the command itself is a variable or placeholder
	}
	cmd := rootCmd
	i := 0
	for ; i < len(toks); i++ {
		next := findSub(cmd, toks[i])
		if next == nil {
			break
		}
		cmd = next
	}
	if i < len(toks) && cmd.HasSubCommands() && !cmd.Runnable() && isWord(toks[i]) {
		return "no command `" + cmd.CommandPath() + " " + toks[i] + "`"
	}
	if i == 0 && len(toks) > 0 && isWord(toks[0]) && cmd == rootCmd {
		return "no command `lore " + toks[0] + "`"
	}
	given := map[string]bool{}
	for _, tok := range toks[i:] {
		name, ok := flagName(tok)
		if !ok {
			continue
		}
		f := lookupFlag(cmd, name)
		if f == nil {
			return "`" + cmd.CommandPath() + "` has no flag " + tok
		}
		given[f.Name] = true
	}
	if strings.ContainsAny(code, "…") || strings.Contains(code, "...") || i == len(toks) {
		// An abbreviated form, or a bare command name naming the command
		// rather than running it (`lore decision add` in a table): required
		// flags are not expected there.
		return ""
	}
	var missing []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if req, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok && len(req) > 0 && req[0] == "true" && !given[f.Name] {
			missing = append(missing, "--"+f.Name)
		}
	})
	if len(missing) > 0 {
		return "`" + cmd.CommandPath() + "` requires " + strings.Join(missing, ", ")
	}
	return ""
}

// docTokens splits a documented command into words, honouring quotes and
// dropping optional-syntax brackets ("[--json]" → "--json").
func docTokens(code string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.Trim(cur.String(), "[]"))
			cur.Reset()
		}
	}
	for _, r := range code {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			cur.WriteRune(r)
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// isWord reports whether a token reads like a subcommand name rather than an
// argument, placeholder, id or flag.
func isWord(tok string) bool {
	return regexp.MustCompile(`^[a-z][a-z-]*$`).MatchString(tok)
}

// flagName extracts the flag name from "--name", "--name=value" or "-n".
func flagName(tok string) (string, bool) {
	tok = strings.Trim(tok, "[]")
	switch {
	case strings.HasPrefix(tok, "--") && len(tok) > 2:
		name, _, _ := strings.Cut(tok[2:], "=")
		name = strings.TrimRight(name, ",")
		return name, regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(name)
	case len(tok) == 2 && tok[0] == '-' && tok[1] != '-':
		return tok[1:], true
	}
	return "", false
}

// findSub returns the direct subcommand named (or aliased) name.
func findSub(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// lookupFlag finds a long or short flag on cmd, its parents' persistent
// flags, or cobra's built-in help/version flags.
func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for _, set := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags(), cmd.PersistentFlags()} {
		if f := set.Lookup(name); f != nil {
			return f
		}
		if len(name) == 1 {
			if f := set.ShorthandLookup(name); f != nil {
				return f
			}
		}
	}
	if name == "help" || name == "h" || (cmd == rootCmd && (name == "version" || name == "v")) {
		return &pflag.Flag{Name: name}
	}
	return nil
}
