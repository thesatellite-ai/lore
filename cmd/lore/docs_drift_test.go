package main

// Docs-vs-CLI drift gate machinery, run against cmd/lore's command tree
// (ported from saas/cmd/cli/docs_drift_test.go, B11, so the gate outlives
// the old binary): the extractor, the invocation checker and their pinning
// tests. The gate over the real docs (TestDocsMatchCLI) lands beside it once
// cmd/lore answers every documented command.
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
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// docsRootCmd is cmd/lore's command tree, built once per test binary.
var docsRootCmd *cobra.Command

// driftRoot builds the tree the way run (main.go) does, over a scratch
// working folder and lore home: every registry operation through
// surface/cli, the host hooks, the shortcuts, the host commands, the merge
// driver and `lore sync hook`. It mirrors run's construction because run
// builds and executes in one step; a command run adds and this misses makes
// the gate fail loudly (a documented command reported missing), never pass
// vacuously.
func driftRoot(t *testing.T) *cobra.Command {
	t.Helper()
	if docsRootCmd != nil {
		return docsRootCmd
	}
	wd, home := t.TempDir(), t.TempDir()
	base, org := wire.StandaloneOptionsOrg(wd, home, wd, version)
	clk := liveClock()
	base.Wall = clk.Now
	hooks := hosthooks.Replace(hosthooks.Default(wd), hosthooks.CompatFlagsHook(hosthooks.CompatOptions{Filed: org.Routed}))
	liveRows := hosthooks.NewLive(hosthooks.LiveConfig{Home: home, WorkDir: wd, Version: version, Clock: clk})
	hooks = append(hooks, liveRows.Hook())
	app, err := wire.New(hosthooks.Options(base, hooks))
	if err != nil {
		t.Fatal(err)
	}
	// The tree outlives this test (cached for the others); the app holds
	// only a scratch cache, released when the test binary exits.
	runner := hosthooks.NewRunner(app, hooks)
	root := cli.Build(app.Registry(), runner, cli.WithInteractive(wire.StdinIsTerminal))
	root.Version = version
	hosthooks.Install(root, runner)
	h := &host{app: app, home: home, live: liveRows}
	mountShortcuts(root)
	root.AddCommand(h.apiCommand(), h.specCommand(), h.versionCommand(), h.initCommand(wd), h.tuiCommand())
	root.AddCommand(hosthooks.MergeDriverCommand(os.Stderr))
	if sync := find(root, opssync.Noun); sync != nil {
		sync.AddCommand(liveRows.SyncHookCommand(os.Stdin))
	}
	// cobra adds `completion` and `help` only when the root executes; add
	// them now so docs that run them are checked like any other command.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	docsRootCmd = root
	return root
}

// binaryName is the command word every documented invocation starts with.
const binaryName = "lore"

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

// Scanner buffer for long markdown lines (wide tables): initial and maximum.
const (
	docLineBufInitial = 64 << 10
	docLineBufMax     = 1 << 20
)

// Placeholders a document uses for "some command here": the invocation is
// a pattern, not a command to check.
const (
	placeholderEllipsis = "..."
	placeholderUnicode  = "…"
)

// cobraRequiredTrue is the value cobra stores under BashCompOneRequiredFlag
// on a flag marked with MarkFlagRequired.
const cobraRequiredTrue = "true"

// Flags cobra adds itself: --help/-h on every command, --version/-v on the
// root (because rootCmd sets Version). They are not in any FlagSet until
// cobra initialises them at execution time.
var cobraBuiltinFlags = map[string]bool{"help": true, "h": true}
var cobraRootBuiltinFlags = map[string]bool{"version": true, "v": true}

// subcommandWord reads like a subcommand name, not an argument or id.
var subcommandWord = regexp.MustCompile(`^[a-z][a-z-]*$`)

// longFlagName is a valid long flag name.
var longFlagName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// shellSubstitutionStandIn replaces a `$( … )` that is not itself a lore
// invocation (`--due=$(date -v+1d +%F)`): one opaque word, so its spaces
// and its ")" do not split or end the lore command around it.
const shellSubstitutionStandIn = "SUBST"

// foldSubstitutions replaces every `$( … )` whose command is not lore with
// shellSubstitutionStandIn. `$(lore …)` is kept: it is an invocation.
func foldSubstitutions(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "$(")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		if strings.HasPrefix(strings.TrimSpace(s[i+2:]), binaryName+" ") {
			b.WriteString(s[:i+2])
			s = s[i+2:]
			continue
		}
		depth, j := 0, i+1
		for ; j < len(s); j++ {
			if s[j] == '(' {
				depth++
			} else if s[j] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		b.WriteString(s[:i] + shellSubstitutionStandIn)
		if j >= len(s) {
			return b.String()
		}
		s = s[j+1:]
	}
}

// annotationGap is a wide gap followed by a plain word: in a reference
// listing it starts a column-aligned description ("lore sync peek
// origin/<b>        another branch, read-only"). A gap followed by a flag, a
// quote, a variable or a placeholder is alignment inside the command
// ("lore task add "Wire FTS5"      --tasklist=$TL") and is kept.
var annotationGap = regexp.MustCompile(`\S\s{3,}[A-Za-z]`)

// dropAnnotation cuts a fenced line where its description starts.
func dropAnnotation(s string) string {
	start := strings.Index(s, binaryName+" ")
	if start < 0 {
		return s
	}
	cmd := s[start:]
	// Alignment between the subcommand words ("lore pattern   add") is not
	// a description: only look after the third word.
	words := 0
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == ' ' && (i == 0 || cmd[i-1] != ' ') {
			words++
			if words == 3 {
				if m := annotationGap.FindStringIndex(cmd[i-1:]); m != nil {
					return s[:start] + cmd[:i-1+m[0]+1]
				}
				break
			}
		}
	}
	return s
}

// deliberateCLIErrorOutput is the pasted output of an example that shows
// lore refusing a wrong flag or command on purpose (a troubleshooting page
// demonstrating "unknown flag"). The command line right above it is the
// error being taught, so it is not checked. Only the parser's own refusals
// count: any other ERROR keeps the command checked. FLAG2: the refusal now
// carries [E_INVALID_INPUT] (exit 20); pages pasted before it lack the code,
// so both spellings count.
var deliberateCLIErrorOutput = regexp.MustCompile(`^ERROR: (\[E_INVALID_INPUT\] )?unknown (flag|shorthand flag|command)\b`)

// deliberateCLIError reports whether a fenced line is such output.
func deliberateCLIError(line string) bool {
	return deliberateCLIErrorOutput.MatchString(strings.TrimSpace(line))
}

// transcriptFenceLangs are the fence languages of a pasted terminal session:
// a line is a command only when it starts with the shell prompt; every other
// line is output. Output can contain "; lore stopped writing to it" or a hint
// "…; lore doctor shows why", which must never be checked as a command.
var transcriptFenceLangs = map[string]bool{"console": true, "shell-session": true}

// transcriptPrompt starts a command line inside a transcript fence.
const transcriptPrompt = "$ "

// codeFence opens and closes a fenced block.
const codeFence = "```"

// fenceLang returns the language of a fence-opening line ("```console" →
// "console").
func fenceLang(line string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), codeFence))
}

// docInvocation is one `lore …` command found in a document.
type docInvocation struct {
	code string
	line int
}

// docInvocations extracts every lore invocation from code in a markdown file.
func docInvocations(t *testing.T, path string) []docInvocation {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read-only
	var out []docInvocation
	inFence, transcript := false, false
	n, start := 0, 0
	pending := ""
	// lastLineFrom is where the previous fenced line's invocations start
	// in out (-1: none), so a pasted "ERROR: unknown flag" right below can
	// withdraw them (deliberateCLIError).
	lastLineFrom := -1
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, docLineBufInitial), docLineBufMax)
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), codeFence) {
			inFence = !inFence
			transcript = inFence && transcriptFenceLangs[fenceLang(line)]
			pending, lastLineFrom = "", -1
			continue
		}
		if inFence && lastLineFrom >= 0 && deliberateCLIError(line) {
			out = out[:lastLineFrom] // an example of the error, not a command to run
		}
		lastLineFrom = -1
		// In a transcript only a prompt line (or the continuation of one,
		// held in pending) is a command; anything else is output.
		if transcript && pending == "" && !strings.HasPrefix(strings.TrimLeft(line, " \t"), transcriptPrompt) {
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
		lineFrom := len(out)
		for _, s := range spans {
			s = foldSubstitutions(s)
			if inFence {
				s = dropAnnotation(s)
			}
			for _, loc := range invocationStart.FindAllStringIndex(s, -1) {
				start := loc[0] + strings.Index(s[loc[0]:], "lore")
				rest := s[start:]
				if end := commandEnd.FindStringIndex(rest[len("lore"):]); end != nil {
					rest = rest[:len("lore")+end[0]]
				}
				out = append(out, docInvocation{code: strings.TrimSpace(rest), line: lineNo})
			}
		}
		if inFence && len(out) > lineFrom {
			lastLineFrom = lineFrom
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// checkInvocation resolves code against the command tree and returns a
// problem description, or "" when it is valid.
func checkInvocation(rootCmd *cobra.Command, code string) string {
	toks := docTokens(code)[1:] // drop "lore"
	if len(toks) > 0 && (strings.ContainsAny(toks[0][:1], "$<") || toks[0] == placeholderEllipsis || toks[0] == placeholderUnicode) {
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
	// A shortcut (`lore move`, bare `lore doctor`) parses its words with
	// the command it forwards to: check the flags against that one.
	if target := forwardTarget(rootCmd, cmd); target != nil {
		cmd = target
	} else if cmd.DisableFlagParsing {
		return "" // a raw-argument command: nothing to check
	}
	given := map[string]bool{}
	var positional []string
	rest := toks[i:]
	// Notation, not arguments: "( …" opens prose ("lore rule edit R-N (or
	// archive + add)"); "/" and "|" separate alternatives ("archive /
	// unarchive <id>", "(--dry-run | --confirm)"). Flags are still checked;
	// the argument count is not.
	notation := false
	for k, tok := range rest {
		if tok == "/" || tok == "|" || strings.HasPrefix(tok, "(") && !strings.HasPrefix(tok, "(--") {
			if strings.HasPrefix(tok, "(") {
				rest = rest[:k]
			}
			notation = true
			break
		}
	}
	for k := 0; k < len(rest); k++ {
		tok := rest[k]
		if strings.HasPrefix(strings.TrimLeft(tok, "[("), "--") && strings.Contains(tok, "|") {
			// "[--all-repos|--master-only|--no-inherit]": one of several flags.
			for _, alt := range strings.Split(strings.Trim(tok, "[]()"), "|") {
				if name, ok := flagName(alt); ok && lookupFlag(rootCmd, cmd, name) == nil {
					return "`" + cmd.CommandPath() + "` has no flag " + alt
				}
			}
			continue
		}
		name, ok := flagName(tok)
		if !ok {
			positional = append(positional, tok)
			continue
		}
		f := lookupFlag(rootCmd, cmd, name)
		if f == nil {
			return "`" + cmd.CommandPath() + "` has no flag " + tok
		}
		given[f.Name] = true
		// "--title value": a flag that takes a value consumes the next word
		// unless it was written "--title=value".
		if !strings.Contains(tok, "=") && flagTakesValue(f) && k+1 < len(rest) {
			k++
		}
	}
	abbreviated := strings.Contains(code, placeholderUnicode) || strings.Contains(code, placeholderEllipsis)
	nameOnly := i == len(toks)
	// Only arguments that ARE given are checked: a reference fragment such as
	// "lore restore --prefer <x>" names a flag without repeating the
	// command's required arguments, while text passed where a command takes
	// none (the bug class this catches) always shows up here.
	if !notation && !abbreviated && !nameOnly && len(positional) > 0 {
		if err := cmd.ValidateArgs(positional); err != nil {
			return "`" + cmd.CommandPath() + "` rejects the arguments " + strings.Join(positional, " ") + ": " + err.Error()
		}
	}
	if abbreviated || nameOnly || notation {
		// An abbreviated form, or a bare command name naming the command
		// rather than running it (`lore decision add` in a table): required
		// flags are not expected there.
		return ""
	}
	var missing []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if req, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok && len(req) > 0 && req[0] == cobraRequiredTrue && !given[f.Name] {
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
	return subcommandWord.MatchString(tok)
}

// flagName extracts the flag name from "--name", "--name=value" or "-n".
func flagName(tok string) (string, bool) {
	tok = strings.Trim(tok, "[]()")
	switch {
	case strings.HasPrefix(tok, "--") && tok != "--":
		name, _, _ := strings.Cut(strings.TrimPrefix(tok, "--"), "=")
		name = strings.TrimRight(name, ",")
		return name, longFlagName.MatchString(name)
	case len(tok) == 2 && tok[0] == '-' && tok[1] != '-':
		return tok[1:], true
	}
	return "", false
}

// flagTakesValue reports whether a flag needs a value (anything but a
// boolean or a flag with an implicit value).
func flagTakesValue(f *pflag.Flag) bool {
	return f.Value != nil && f.Value.Type() != "bool" && f.NoOptDefVal == ""
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
func lookupFlag(rootCmd, cmd *cobra.Command, name string) *pflag.Flag {
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
	if cobraBuiltinFlags[name] || (cmd == rootCmd && cobraRootBuiltinFlags[name]) {
		return &pflag.Flag{Name: name}
	}
	return nil
}

// TestDeliberateCLIErrorExemption pins the exemption rule: only a fenced
// command whose very next line is lore's own unknown-flag/command refusal is
// withdrawn; every other command (and other errors) stays checked.
func TestDeliberateCLIErrorExemption(t *testing.T) {
	t.Parallel()
	doc := "```\n" +
		"$ lore rule list --colour never\n" +
		"ERROR: unknown flag: --colour\n" +
		"$ lore rule list --json\n" +
		"ERROR: [E_NOT_PROJECT_ROOT] not a project\n" +
		"$ lore nosuchverb\n" +
		"\n" +
		"ERROR: unknown command \"nosuchverb\" for \"lore\"\n" +
		"$ lore task list --nope\n" +
		"ERROR: [E_INVALID_INPUT] unknown flag: --nope\n" +
		"$ lore memory list\n" +
		"```\n" +
		"ERROR: unknown flag: --x\n"
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, inv := range docInvocations(t, path) {
		got = append(got, inv.code)
	}
	want := []string{"lore rule list --json", "lore nosuchverb", "lore memory list"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("invocations = %q, want %q", got, want)
	}
}

// TestCobraBuiltinsAreCommands pins that `lore completion` and `lore help`
// resolve once cobra's defaults are initialised (docs run both).
func TestCobraBuiltinsAreCommands(t *testing.T) {
	rootCmd := driftRoot(t)
	for _, c := range []string{"lore completion bash", "lore help task"} {
		if p := checkInvocation(rootCmd, c); p != "" {
			t.Errorf("checkInvocation(%q) = %q, want valid", c, p)
		}
	}
}

// TestTranscriptOutputIsNotACommand pins that inside a console transcript
// only prompt lines are commands: output such as "ERROR: … deleted; lore
// stopped writing to it" or "lore live: landed on main" is never checked,
// while a command after `;` on a prompt line, a continued prompt line, and
// every line of a bash fence still are. The output case once failed the gate
// and forced a docs example to hide the failing write it was meant to show.
func TestTranscriptOutputIsNotACommand(t *testing.T) {
	t.Parallel()
	doc := "```console\n" +
		"# alice, in billing\n" +
		"$ lore task add 'close the books'\n" +
		"ERROR: [E_READ_ONLY] the remote live ref was deleted; lore stopped writing to it\n" +
		"  hint: run `lore live restore`; lore keeps a copy\n" +
		"lore live: landed on main (abc1234)\n" +
		"$ git fetch -q; lore rule list\n" +
		"$ lore memory list \\\n" +
		"  --json\n" +
		"```\n" +
		"```bash\n" +
		"cd repo; lore decision list\n" +
		"lore sync\n" +
		"```\n"
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, inv := range docInvocations(t, path) {
		got = append(got, inv.code)
	}
	want := []string{
		"lore task add 'close the books'",
		"lore rule list",
		"lore memory list  --json", // continuation join keeps the line's space
		"lore decision list",
		"lore sync",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("invocations = %q, want %q", got, want)
	}
}

// forwardTarget returns the command a shortcut forwards to (shortcuts.go):
// for a noun made to run a default verb (defaultVerb) it is the subcommand
// whose Short the group took over; for a top-level word (shortcutOf) it is
// the one command elsewhere in the tree with the same Long text that parses
// its own flags. nil when cmd parses its own flags or no target is found.
// TestForwardTargets pins both shapes against the real mountShortcuts.
func forwardTarget(root, cmd *cobra.Command) *cobra.Command {
	if !cmd.DisableFlagParsing || cmd.RunE == nil {
		return nil
	}
	for _, sub := range cmd.Commands() {
		if sub.Short == cmd.Short && sub.RunE != nil && !sub.DisableFlagParsing {
			return sub
		}
	}
	if cmd.Long == "" {
		return nil
	}
	var found *cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub != cmd && sub.Long == cmd.Long && !sub.DisableFlagParsing && sub.RunE != nil {
				found = sub
			}
			walk(sub)
		}
	}
	walk(root)
	return found
}

// TestForwardTargets pins forwardTarget over the real shortcut wiring:
// bare `lore doctor` checks `doctor run`'s flags, `lore move` checks
// `row move`'s, and a command that parses its own flags is its own.
func TestForwardTargets(t *testing.T) {
	t.Parallel()
	leaf := func(use, short, long string, flags ...string) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Long: long, RunE: func(*cobra.Command, []string) error { return nil }}
		for _, f := range flags {
			c.Flags().Bool(f, false, "")
		}
		return c
	}
	root := &cobra.Command{Use: binaryName}
	doctor := &cobra.Command{Use: "doctor"}
	doctorRun := leaf("run", "Check the stores", "", "json", "fix")
	doctor.AddCommand(doctorRun)
	row := &cobra.Command{Use: "row"}
	rowMove := leaf("move <id>", "Move a row", "Move any row to another store", "to")
	row.AddCommand(rowMove)
	plain := leaf("plain", "own flags", "", "x")
	root.AddCommand(doctor, row, plain)
	mountShortcuts(root)
	for _, tc := range []struct {
		path []string
		want *cobra.Command
	}{
		{[]string{"doctor"}, doctorRun},
		{[]string{"move"}, rowMove},
		{[]string{"plain"}, nil},
		{[]string{"row", "move"}, nil},
	} {
		c := find(root, tc.path...)
		if c == nil {
			t.Fatalf("%v: not mounted", tc.path)
		}
		if got := forwardTarget(root, c); got != tc.want {
			t.Errorf("forwardTarget(%v) = %v, want %v", tc.path, got, tc.want)
		}
	}
	for code, wantOK := range map[string]bool{
		"lore doctor --json --fix": true,
		"lore doctor --nope":       false,
		"lore move mem_1 --to me":  true,
		"lore move mem_1 --from x": false,
	} {
		if p := checkInvocation(root, code); (p == "") != wantOK {
			t.Errorf("checkInvocation(%q) = %q, want ok=%v", code, p, wantOK)
		}
	}
}
