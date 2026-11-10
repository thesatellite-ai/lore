package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Reserved flags every generated command may carry. An operation field may
// not use these names (Mount refuses it), so --json and --yes mean one thing
// on every command (CLI_REFERENCE 1.2).
const (
	// FlagJSON prints the Out struct (or the error object) as JSON.
	FlagJSON = "json"
	// FlagYes answers a confirmation without a terminal; it is added only to
	// operations whose Spec.Confirm can ask.
	FlagYes = "yes"
	// flagHelp is cobra's own help flag.
	flagHelp = "help"
)

// RootName is the binary name Build gives the root command.
const RootName = "lore"

// annotationOwner marks commands this package created, so Mount can reuse
// its own noun groups but never take over a command the old binary owns.
const (
	annotationOwner = "lore.surface.cli"
	annotationValue = "op"
)

// saysRequired reports whether a field's own description already ends in
// "(required)": twelve add commands printed "title (required) (required)"
// because their desc said it and the Required mark added it again.
func saysRequired(desc string) bool {
	return strings.HasSuffix(strings.TrimSpace(desc), strings.TrimSpace(requiredSuffix))
}

// Help text pieces.
const (
	argsHeading    = "Arguments:"
	requiredSuffix = " (required)"
	confirmPrompt  = " [y/N] "
	notConfirmed   = "not confirmed; nothing written"
)

// yesAnswers are the replies that confirm at the [y/N] prompt; anything else
// (including an empty line) is no, so Enter never confirms by accident.
var yesAnswers = []string{"y", "yes"}

// Option configures Build and Mount.
type Option func(*config)

// config is the injected environment of the generated commands. Nothing in
// this package reads os.Stdin, the environment or a terminal by itself.
type config struct {
	// interactive reports whether a person can answer a prompt now. The
	// default is false (fail closed: a confirming command then exits
	// E_NEEDS_CONFIRM instead of waiting on a pipe).
	interactive func() bool
	// caller resolves the base Caller (actor, agent) for one command run.
	caller func(context.Context) (op.Caller, error)
	// color is the palette of one output stream (colour.go); the default
	// is off for every stream.
	color func(Stream) Palette
	// args are the process arguments (without the program name) the
	// command tree is run with (WithArgs). Only a usage error reads them:
	// it is printed before (or instead of) cobra parsing --json, so
	// whether the caller asked for JSON comes from the raw words
	// (JSONRequested). Nil: a usage error is always text.
	args []string
}

// WithArgs injects the raw arguments the binary runs the tree with (the
// ones it passes to SetArgs), so a usage error under --json prints the
// JSON error envelope on stdout like every other error (usageError).
func WithArgs(args []string) Option {
	return func(c *config) { c.args = args }
}

// WithInteractive injects the TTY check (the binary passes an isatty test of
// stdin and stdout). It sets Caller.Interactive and enables the [y/N] prompt.
func WithInteractive(f func() bool) Option {
	return func(c *config) { c.interactive = f }
}

// WithCaller injects how the actor and agent of a run are resolved
// (identity, agent detection: CLI_REFERENCE 1.6). The CLI then sets
// Interactive and Yes on the returned Caller. An error from f is printed
// like any operation error.
func WithCaller(f func(context.Context) (op.Caller, error)) Option {
	return func(c *config) { c.caller = f }
}

func newConfig(opts []Option) config {
	c := config{
		interactive: func() bool { return false },
		caller:      func(context.Context) (op.Caller, error) { return op.Caller{}, nil },
		color:       func(Stream) Palette { return Palette{} },
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Build returns a fresh `lore` root command holding one command per
// registered operation, for the future cmd/lore binary. Run it with
// root.ExecuteContext and pass the error to ExitCode.
//
// It panics when Mount fails, which on a fresh root only happens for a
// programmer error (an operation field named like a reserved flag): the
// same startup-panic rule as op.Define.
func Build(reg *op.Registry, runner op.Runner, opts ...Option) *cobra.Command {
	// FLAG2: the root refuses an unknown command word like a group does
	// (usageError: E_INVALID_INPUT, exit 20) instead of letting cobra print
	// its own "Error: unknown command" with exit 1; bare, it prints help.
	cfg := newConfig(opts)
	root := &cobra.Command{
		Use:           RootName,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return usageError(cmd, cfg, fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath()))
		},
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return cfg.flagError(cmd, err, nil) })
	acceptJSON(root)
	if err := Mount(root, reg, runner, opts...); err != nil {
		panic(err)
	}
	return root
}

// Mount adds one command per registered operation under root: `<noun>
// [<noun>…] <verb>`. The old binary (saas/cmd/cli) calls it during the
// strangler migration to serve the migrated nouns from the registry.
//
// It refuses (E_CONFIG_INVALID, nothing added) when an operation's command
// already exists under root, or when a noun word is a runnable command this
// package did not create: the old command for a noun must be removed before
// the new one is mounted, so one word never has two meanings. A noun that is
// a pure group (no Run) is reused.
func Mount(root *cobra.Command, reg *op.Registry, runner op.Runner, opts ...Option) error {
	cfg := newConfig(opts)
	entries := reg.Entries()
	for _, e := range entries {
		if err := checkMount(root, e); err != nil {
			return err
		}
	}
	for _, e := range entries {
		path := op.CLIPath(e.Spec().Name)
		parent := root
		for _, w := range path[:len(path)-1] {
			parent = group(parent, w, cfg)
		}
		parent.AddCommand(command(e, path[len(path)-1], runner, cfg))
	}
	hideEmptyGroups(root)
	return nil
}

// hideEmptyGroups hides every group this package made whose subcommands
// are all hidden. A group is runnable (it refuses an unknown sub-word), so
// cobra would otherwise list it in help even with nothing to offer.
func hideEmptyGroups(c *cobra.Command) {
	for _, s := range c.Commands() {
		if !ours(s) || !s.HasSubCommands() {
			continue
		}
		hideEmptyGroups(s)
		if !s.HasAvailableSubCommands() {
			s.Hidden = true
		}
	}
}

// checkMount verifies e can be mounted under root without a clash.
func checkMount(root *cobra.Command, e op.Entry) error {
	name := e.Spec().Name
	for _, f := range e.InputFields() {
		if f.Flag == FlagJSON || f.Flag == FlagYes || f.Flag == flagHelp {
			return errs.New(errs.ConfigInvalid, "cli: "+name+": flag --"+f.Flag+" is reserved")
		}
	}
	path := op.CLIPath(name)
	cur := root
	for i, w := range path {
		next := child(cur, w)
		if next == nil {
			return nil
		}
		last := i == len(path)-1
		if last || (next.Runnable() && !ours(next)) {
			return errs.New(errs.ConfigInvalid, "cli: "+name+": command `"+next.CommandPath()+"` already exists")
		}
		cur = next
	}
	return nil
}

// child returns the direct sub-command of c named name, or nil.
func child(c *cobra.Command, name string) *cobra.Command {
	for _, s := range c.Commands() {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// ours reports whether this package created c.
func ours(c *cobra.Command) bool { return c.Annotations[annotationOwner] == annotationValue }

// Generated reports whether c is a command Mount built from an operation
// (or a group of them), as opposed to one the host added beside them.
func Generated(c *cobra.Command) bool { return ours(c) }

// group returns the noun command name under parent, creating it.
func group(parent *cobra.Command, name string, cfg config) *cobra.Command {
	if c := child(parent, name); c != nil {
		return c
	}
	// Help from the noun table (nouns.go); a group missing from it keeps the
	// placeholder, which cmd/lore TestEveryNounGroupHasHelp refuses.
	full := strings.TrimSpace(parent.CommandPath() + " " + name)
	doc, ok := nounDoc(strings.TrimSpace(strings.TrimPrefix(full, RootName)))
	if !ok {
		doc = NounDoc{Short: full + genericGroupSuffix}
	}
	g := &cobra.Command{
		Use:           name,
		Short:         doc.Short,
		Long:          doc.Long,
		Annotations:   map[string]string{annotationOwner: annotationValue},
		SilenceErrors: true,
		SilenceUsage:  true,
		// A group is runnable only to refuse a word that names none of its
		// subcommands (cobra would print the group's help with exit 0):
		// bare, it prints help as before.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return usageError(cmd, cfg, fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath()))
		},
	}
	acceptJSON(g)
	parent.AddCommand(g)
	return g
}

// acceptJSON gives a group (or the root) a hidden local --json flag. A
// group only refuses an unknown word, so the flag changes nothing it does;
// without it `lore memory nope --json` was refused as "unknown flag:
// --json" instead of naming the unknown command, the real mistake
// (usageError then prints that refusal as the JSON envelope).
func acceptJSON(g *cobra.Command) {
	_ = g.Flags().Bool(FlagJSON, false, "print the error as JSON")
	_ = g.Flags().MarkHidden(FlagJSON) // the flag was just defined: MarkHidden cannot fail
}

// command builds the leaf command of one operation.
func command(e op.Entry, verb string, runner op.Runner, cfg config) *cobra.Command {
	s := e.Spec()
	fields := e.InputFields()
	c := &cobra.Command{
		Use:           usage(verb, fields),
		Short:         s.Summary,
		Long:          long(s, fields),
		Example:       examples(s.Examples),
		Hidden:        s.Hidden,
		Args:          cobra.ArbitraryArgs, // counted by op.ArgsDecoder
		Annotations:   map[string]string{annotationOwner: annotationValue},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	values := map[string]*rawFlag{}
	aliases := map[string][]string{} // flag → its other spellings
	// pflag parses in order and appends each positional to Args() as it
	// goes, so at Set time len(Args()) is the number of positionals typed
	// before the value (the group routing of op.ArgsDecoder.FlagPos).
	seen := func() int { return len(c.Flags().Args()) }
	for _, f := range fields {
		if f.Flag == "" {
			continue
		}
		rf := &rawFlag{typ: f.Type, at: seen}
		values[f.Flag] = rf
		desc := f.Desc
		if f.Required && !saysRequired(desc) {
			desc += requiredSuffix
		}
		if len(f.Aliases) > 0 {
			desc += " (also --" + strings.Join(f.Aliases, ", --") + ")"
		}
		fl := c.Flags().VarPF(rf, f.Flag, f.Short, desc)
		if f.Type == op.TypeBool {
			fl.NoOptDefVal = boolNoValue
		}
		// FB-FLAG-STORE: an alias parses into the same value, so a repeat
		// across spellings is still "takes one value"; it is hidden, the
		// flag's own help line names it.
		for _, a := range f.Aliases {
			al := c.Flags().VarPF(rf, a, "", desc)
			al.NoOptDefVal = fl.NoOptDefVal
			al.Hidden = true
		}
		aliases[f.Flag] = f.Aliases
	}
	asJSON := c.Flags().Bool(FlagJSON, false, "print the result (or the error) as JSON")
	asksFirst := s.Confirm.CanAsk()
	yes := new(bool)
	if asksFirst {
		c.Flags().BoolVar(yes, FlagYes, false, "answer yes to the confirmation without a terminal")
	}
	sf := secretFlags(c, s, values)
	// A cobra flag parse error (unknown flag, unknown shorthand flag,
	// missing value) is a usage error: exit 1 (CLI_REFERENCE 1.5).
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return cfg.flagError(cmd, err, fields)
	})
	c.RunE = func(cmd *cobra.Command, args []string) error {
		flags := map[string][]string{}
		pos := map[string][]int{}
		for name, rf := range values {
			if changed(cmd, name, aliases[name]) {
				flags[name] = rf.vals
				pos[name] = rf.pos
			}
		}
		res, err := run(cmd, e, runner, cfg, op.ArgsDecoder{Args: args, Flags: flags, FlagPos: pos}, *yes, sf.choice(cmd))
		out := cmd.OutOrStdout()
		if err != nil {
			return &exitError{code: printError(out, cmd.ErrOrStderr(), err, *asJSON, cfg.color(StreamErr)), cause: err}
		}
		if *asJSON {
			err = printJSON(out, res)
		} else {
			err = printHuman(out, res, cfg.color(StreamOut))
		}
		if err != nil {
			return &exitError{code: printError(out, cmd.ErrOrStderr(), err, *asJSON, cfg.color(StreamErr)), cause: err}
		}
		// P3-ORG-G-3: an output printed in full may still fail
		// (op.Failer, whoami's org refusal): the error line goes to
		// stderr in human mode; with --json the data already holds it.
		if ferr := res.Failure(); ferr != nil {
			if *asJSON {
				return &exitError{code: errs.EnvelopeOf(ferr).Error.Exit, cause: ferr}
			}
			return &exitError{code: printError(out, cmd.ErrOrStderr(), ferr, false, cfg.color(StreamErr)), cause: ferr}
		}
		// P3-MISC-8: a successful result may carry its own exit status
		// (op.ExitStatuser, lore doctor's 1 degraded / 2 broken); it is
		// already printed, so only the status goes back.
		if code := res.ExitStatus(); code != 0 {
			return &exitError{code: code}
		}
		return nil
	}
	return c
}

// run calls the Runner and, for an interactive caller, answers what it
// asks:
//
//	Run ─▶ E_NEEDS_CONFIRM? ── no / not a terminal ─▶ return as is
//	        │ findings, no secret choice yet ─▶ secret menu (2.1) ─▶ Run with the choice
//	        │ otherwise, no --yes ───────────▶ [y/N] ─▶ Run with Yes
//	        └ (the re-run may ask again: the not-a-secret confirm)
//
// Why here: the Runner decides THAT an operation asks; only the surface can
// talk to a person. A non-interactive caller gets the error back unchanged
// (the agent asks its human and re-runs with the flags). One reader serves
// every prompt of the run, so no typed line is lost between prompts.
func run(cmd *cobra.Command, e op.Entry, runner op.Runner, cfg config, d op.Decoder, yes bool, secret op.SecretChoice) (op.Result, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	caller, err := cfg.caller(ctx)
	if err != nil {
		return op.Result{}, err
	}
	caller.Interactive = cfg.interactive()
	caller.Yes = yes
	caller.Secret = secret
	res, err := runner.Run(ctx, e, d, caller)
	in := bufio.NewReader(cmd.InOrStdin())
	for range maxAsks {
		if !errs.Has(err, errs.NeedsConfirm) || !caller.Interactive {
			return res, err
		}
		body := errs.EnvelopeOf(err).Error
		switch {
		case len(body.Findings) > 0 && !caller.Secret.Any():
			// SECRETS 2.1 / FB-SEC-2: the finding needs a choice, never a
			// yes; cancel, an empty answer or end of input writes nothing.
			ch, ok := secretMenu(in, cmd.ErrOrStderr(), body.Findings)
			if !ok {
				return op.Result{}, errs.New(errs.NeedsConfirm, notConfirmed)
			}
			caller.Secret = ch
		case caller.Yes:
			return res, err
		default:
			// the question as written for the person; the message carries
			// the package prefix (engine: …) and is the fallback only
			q := body.Question
			if q == "" {
				q = body.Message
			}
			if !ask(in, cmd.ErrOrStderr(), q) {
				return op.Result{}, errs.New(errs.NeedsConfirm, notConfirmed)
			}
			caller.Yes = true
		}
		res, err = runner.Run(ctx, e, d, caller)
	}
	return res, err
}

// maxAsks bounds the prompts of one run: a secret choice, then its
// not-a-secret confirm, then the operation's own confirm. A runner that
// keeps asking past that gets its last answer returned, never a loop.
const maxAsks = 3

// ask prints question with [y/N] and reads one line.
func ask(in *bufio.Reader, out io.Writer, question string) bool {
	_, _ = io.WriteString(out, question+confirmPrompt)
	line, ok := readLine(in)
	return ok && slices.Contains(yesAnswers, strings.ToLower(line))
}

// readLine reads one trimmed line; ok is false at end of input with
// nothing typed.
func readLine(in *bufio.Reader) (string, bool) {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// positional returns the arg fields ordered by index.
func positional(fields []op.FieldInfo) []op.FieldInfo {
	var out []op.FieldInfo
	for _, f := range fields {
		if f.Arg != op.NoArg {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b op.FieldInfo) int { return a.Arg - b.Arg })
	return out
}

// usage is the cobra Use line: the verb, then `<arg>` for a required
// positional and `[arg]` for an optional one.
func usage(verb string, fields []op.FieldInfo) string {
	parts := []string{verb}
	for _, f := range positional(fields) {
		name := f.Name
		if f.Rest {
			name += "..."
		}
		if f.Required {
			parts = append(parts, "<"+name+">")
		} else {
			parts = append(parts, "["+name+"]")
		}
	}
	return strings.Join(parts, " ")
}

// long is the --help body: the summary and the positional arguments.
func long(s op.Spec, fields []op.FieldInfo) string {
	args := positional(fields)
	if len(args) == 0 {
		return s.Summary
	}
	var b strings.Builder
	b.WriteString(s.Summary + "\n\n" + argsHeading + "\n")
	for _, f := range args {
		b.WriteString("  " + f.Name + "  " + f.Desc)
		if f.Required && !saysRequired(f.Desc) {
			b.WriteString(requiredSuffix)
		}
		b.WriteString("\n")
		if f.Group {
			b.WriteString(groupHelp(f))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// groupHelp is the --help line under a repeated group argument: which
// flags an item takes and where they apply (P3-TASK-1).
func groupHelp(f op.FieldInfo) string {
	fl := f.GroupFlags()
	if len(fl) == 0 {
		return ""
	}
	for i, n := range fl {
		fl[i] = "--" + n
	}
	return "    " + strings.Join(fl, " ") + " after an item apply to that item only; before the first item, to every item\n"
}

// examples is the --help Examples block.
func examples(ex []op.Example) string {
	lines := make([]string, 0, 2*len(ex))
	for _, x := range ex {
		if x.Title != "" {
			lines = append(lines, "  # "+x.Title)
		}
		lines = append(lines, "  "+x.CLI)
	}
	return strings.Join(lines, "\n")
}

// usageError prints a cobra usage error (unknown flag, unknown shorthand
// flag, unknown command) as one line on stderr, `ERROR: [E_INVALID_INPUT]
// <cobra's message>`, and returns it as ExitUsage already printed.
//
// FLAG2: a usage error is an invalid argument, so it carries
// E_INVALID_INPUT and exits 20 (bad_input), not v0.1.17's bare line with
// exit 1; this supersedes CLI_REFERENCE 1.5. Still no "Run '… --help'"
// line. Under --json (found in the raw arguments, cfg.args: the flags may
// not have parsed far enough for cobra to know) it is the same JSON error
// envelope on stdout every other error prints, so a script reading stdout
// always gets one document.
func usageError(cmd *cobra.Command, cfg config, err error) error {
	return printUsage(cmd, cfg, errs.Wrap(UsageCode, err.Error(), err))
}

// changed reports whether the flag was given under its name or an alias.
func changed(cmd *cobra.Command, name string, aliases []string) bool {
	if cmd.Flags().Changed(name) {
		return true
	}
	for _, a := range aliases {
		if cmd.Flags().Changed(a) {
			return true
		}
	}
	return false
}

// flagError is usageError for a flag that did not parse (unknown flag,
// unknown shorthand, a bad value: cobra's SetFlagErrorFunc), plus a second
// line naming the right flag when lore knows what the unknown one means
// (flagHint), else where the command's flags are listed:
//
//	ERROR: [E_INVALID_INPUT] unknown flag: --colour
//	  hint: lore memory list --help lists its flags
//
// Exit 20 and the --json rule are usageError's.
func (cfg config) flagError(cmd *cobra.Command, err error, fields []op.FieldInfo) error {
	hint := cmd.CommandPath() + " --help lists its flags"
	if h := flagHint(cmd.CommandPath(), unknownFlag(err), fields); h != "" {
		hint = h
	}
	return printUsage(cmd, cfg, errs.Wrap(UsageCode, err.Error(), err).WithHint(hint))
}

// unknownFlagPrefix is pflag's message for a long flag it does not know.
const unknownFlagPrefix = "unknown flag: --"

// unknownFlag is the name pflag refused as unknown ("" for another parse
// error).
func unknownFlag(err error) string {
	name, ok := strings.CutPrefix(err.Error(), unknownFlagPrefix)
	if !ok {
		return ""
	}
	return name
}

// flagMeanings are the flag spellings whose meaning lore knows across
// commands: each names the others that mean the same thing. The store a
// row is written to or read from is --to (v0.1.17's add spelling) or
// --store (list and search); FB-FLAG-STORE.
var flagMeanings = map[string][]string{
	"to":    {"store"},
	"store": {"to"},
}

// storeFlags are the spellings of the store flag.
var storeFlags = []string{"to", "store"}

// flagHint names the right flag for an unknown one whose meaning lore
// knows: the command's store field (json name to or store; sync's --to
// is a branch) under another spelling, or, for a store flag
// on a command that takes none, why ("" when lore knows nothing better
// than --help).
func flagHint(path, name string, fields []op.FieldInfo) string {
	if name == "" {
		return ""
	}
	for _, other := range flagMeanings[name] {
		for _, f := range fields {
			if slices.Contains(storeFlags, f.Name) && slices.Contains(f.Spellings(), other) {
				return path + " takes --" + f.Flag + ", not --" + name
			}
		}
	}
	if slices.Contains(storeFlags, name) && slices.ContainsFunc(fields, isIDArg) {
		return path + " takes no store flag: it finds the row by its id in every attached store (" + path + " --help lists its flags)"
	}
	return ""
}

// isIDArg reports whether f is a row id positional (show, edit, delete).
func isIDArg(f op.FieldInfo) bool { return f.Arg != op.NoArg && f.Name == "id" }

// printUsage prints a usage error through printError (text on stderr, or
// the envelope on stdout when the raw arguments ask for --json) and
// returns it as ExitUsage. Colour stays off: --color may not have parsed.
func printUsage(cmd *cobra.Command, cfg config, e *errs.Error) error {
	_ = printError(cmd.OutOrStdout(), cmd.ErrOrStderr(), e, JSONRequested(cfg.args), Palette{})
	return &exitError{code: ExitUsage, cause: e}
}
