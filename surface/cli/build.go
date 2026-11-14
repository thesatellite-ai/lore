package cli

import (
	"bufio"
	"context"
	"io"
	"regexp"
	"slices"
	"strconv"
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
	// FlagYes answers a confirmation without a terminal; it is added to
	// every write operation and to any whose Spec.Confirm can ask.
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
	// actorNamer names actor ids in human output (WithActorNamer; nil =
	// ids as stored).
	actorNamer func(context.Context) func(string) string
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
			return unknownWord(cmd, cfg, args[0])
		},
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return cfg.flagError(cmd, err, nil) })
	root.SetHelpCommand(helpCommand(cfg))
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
			return unknownWord(cmd, cfg, args[0])
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
		Annotations:   map[string]string{annotationOwner: annotationValue, AnnotationArgFlags: argFlags(fields)},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	values := map[string]*rawFlag{}
	aliases := map[string][]string{} // flag → its other spellings
	// pflag parses in order and appends each positional to Args() as it
	// goes, so at Set time len(Args()) is the number of positionals typed
	// before the value (the group routing of op.ArgsDecoder.FlagPos).
	seen := func() int { return len(c.Flags().Args()) }
	typed := 0
	next := func() int { typed++; return typed }
	for _, f := range fields {
		if f.Flag == "" {
			continue
		}
		rf := &rawFlag{typ: f.Type, at: seen, next: next}
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
		fl.Hidden = f.Legacy() // parsed for old scripts, never offered in --help
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
	negs := switchPairs(c, fields, values, aliases)
	asJSON := c.Flags().Bool(FlagJSON, false, "print the result (or the error) as JSON")
	negs[FlagJSON] = boolNeg(c, FlagJSON, asJSON)
	// Every write takes --yes (owner report 2026-11-12: `lore member edit
	// --yes` was "unknown flag"): on a write that never asks it is a no-op,
	// so a script can pass it to every write command alike.
	asksFirst := s.Confirm.CanAsk() || s.Permission == op.PermWrite
	yes := new(bool)
	if asksFirst {
		c.Flags().BoolVar(yes, FlagYes, false, "answer yes to the confirmation without a terminal")
		negs[FlagYes] = boolNeg(c, FlagYes, yes)
	}
	sf := secretFlags(c, s, values)
	// A cobra flag parse error (unknown flag, unknown shorthand flag,
	// missing value) is a usage error: exit 1 (CLI_REFERENCE 1.5).
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return cfg.flagError(cmd, err, fields)
	})
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := straySwitchWord(cmd, cfg.args, args); err != nil {
			return printUsage(cmd, cfg, err)
		}
		if err := contradictingSwitch(cmd, negs, aliases); err != nil {
			return printUsage(cmd, cfg, err)
		}
		if err := clearAndSet(fields, values, aliases, cmd); err != nil {
			return printUsage(cmd, cfg, err)
		}
		flags := map[string][]string{}
		pos := map[string][]int{}
		seq := map[string][]int{}
		for name, rf := range values {
			if changed(cmd, name, aliases[name]) {
				flags[name] = rf.vals
				pos[name] = rf.pos
				seq[name] = rf.seq
			}
		}
		res, err := run(cmd, e, runner, cfg, op.ArgsDecoder{Args: args, Flags: flags, FlagPos: pos, FlagSeq: seq, AfterSwitch: switchWords(cmd, cfg.args, args)}, *yes, sf.choice(cmd))
		out := cmd.OutOrStdout()
		if err != nil {
			return &exitError{code: cfg.printNamedError(cmd, err, *asJSON), cause: err}
		}
		if *asJSON {
			err = printJSON(out, res)
		} else {
			var hb strings.Builder
			if err = printHuman(&hb, res, cfg.color(StreamOut), cmd.CommandPath()); err == nil {
				_, err = io.WriteString(out, cfg.nameActors(cmd.Context(), hb.String()))
			}
		}
		if err != nil {
			return &exitError{code: cfg.printNamedError(cmd, err, *asJSON), cause: err}
		}
		// P3-ORG-G-3: an output printed in full may still fail
		// (op.Failer, whoami's org refusal): the error line goes to
		// stderr in human mode; with --json the data already holds it.
		if ferr := res.Failure(); ferr != nil {
			if *asJSON {
				return &exitError{code: errs.EnvelopeOf(ferr).Error.Exit, cause: ferr}
			}
			return &exitError{code: cfg.printNamedError(cmd, ferr, false), cause: ferr}
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

// positional returns the arg fields still shown as positionals, ordered
// by index: none but a repeated group with no flag of its own. U-4: a
// positional with a flag (Named) is shown as its flag, an argof one
// nowhere; both stay quiet aliases.
func positional(fields []op.FieldInfo) []op.FieldInfo {
	var out []op.FieldInfo
	for _, f := range fields {
		if f.Arg != op.NoArg && !f.Named && f.ArgOf == "" {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b op.FieldInfo) int { return a.Arg - b.Arg })
	return out
}

// usage is the cobra Use line: the verb, then the named positionals
// (U-4) as `--flag <name>` when required and `[--flag <name>]` when not,
// then any positional still shown.
func usage(verb string, fields []op.FieldInfo) string {
	parts := []string{verb}
	for _, f := range op.NamedArgs(fields) {
		w := "--" + f.Flag + " <" + f.Name + ">"
		if f.Required {
			parts = append(parts, w)
		} else {
			parts = append(parts, "["+w+"]")
		}
	}
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

// long is the --help body: the summary and the positional arguments still
// shown (a named one is listed under Flags).
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

// switchPairs adds the off spelling --no-<switch> of every bool field
// flag of c (mac:named-no-pairs) and returns them by switch name. A switch
// that already has a field of that name (--live / --no-live), a switch
// that is itself a --no- word, and a legacy flag get none. The pair is
// hidden: --help lists the switch, and the stray-word refusal (`--force
// false`) names --no-<switch> as the way to turn it off. It parses into
// the switch's own value and counts as typing the switch (aliases).
func switchPairs(c *cobra.Command, fields []op.FieldInfo, values map[string]*rawFlag, aliases map[string][]string) map[string]*negFlag {
	taken := map[string]bool{}
	for _, f := range fields {
		taken[f.Flag] = true
		for _, a := range f.Aliases {
			taken[a] = true
		}
	}
	negs := map[string]*negFlag{}
	for _, f := range fields {
		if f.Flag == "" || f.Type != op.TypeBool || f.Legacy() || strings.HasPrefix(f.Flag, negPrefix) || taken[negPrefix+f.Flag] || c.Flags().Lookup(negPrefix+f.Flag) != nil {
			continue
		}
		rf := values[f.Flag]
		negs[f.Flag] = addNeg(c, f.Flag, func(on bool) error { return rf.Set(strconv.FormatBool(on)) })
		aliases[f.Flag] = append(aliases[f.Flag], negPrefix+f.Flag)
	}
	return negs
}

// addNeg adds the hidden --no-<name> of switch name to c; to receives the
// switch's resulting state.
func addNeg(c *cobra.Command, name string, to func(on bool) error) *negFlag {
	n := &negFlag{to: to}
	fl := c.Flags().VarPF(n, negPrefix+name, "", "turn --"+name+" off")
	fl.NoOptDefVal = boolNoValue
	fl.Hidden = true
	return n
}

// boolNeg is addNeg for a switch held in a plain bool (--yes, --json).
func boolNeg(c *cobra.Command, name string, b *bool) *negFlag {
	return addNeg(c, name, func(on bool) error { *b = on; return nil })
}

// contradictingSwitch refuses a switch typed both on and off (`--force
// --no-force`): naming both is a mistake, never "the last one wins".
func contradictingSwitch(cmd *cobra.Command, negs map[string]*negFlag, aliases map[string][]string) *errs.Error {
	for name, n := range negs {
		if !n.set {
			continue
		}
		on := cmd.Flags().Changed(name)
		for _, a := range aliases[name] {
			if a != negPrefix+name && cmd.Flags().Changed(a) {
				on = true
			}
		}
		if on {
			return errs.New(UsageCode, "--"+name+" and --"+negPrefix+name+" contradict each other").
				WithHint("give --" + name + " to turn it on or --" + negPrefix + name + " to turn it off, not both")
		}
	}
	return nil
}

// clearPrefixes are the flag-name prefixes of an op's switch that empties
// or turns off the field its base flag sets (`--clear-due` / `--due`,
// `--no-parent` / `--parent`, `--no-internal` / `--internal`).
var clearPrefixes = []string{"clear-", "no-"}

// notClearPairs are switches whose name looks like a clearing pair but
// that act on something else: --no-meta stops the copy of a --supersedes
// row's metadata, so it goes with --meta (the add's own keys); without
// --supersedes it is refused by the add (metalist.NoMetaNeedsSupersedes).
var notClearPairs = []string{"no-meta"}

// clearAndSet refuses an op's clearing switch typed together with the flag
// that sets the same field, naming both (devco:L-6: `task edit --clear-due
// --due 2026-12-01` dropped --due silently). A switch given as =false
// clears nothing and passes; a bool base given as =false sets nothing.
func clearAndSet(fields []op.FieldInfo, values map[string]*rawFlag, aliases map[string][]string, cmd *cobra.Command) *errs.Error {
	on := func(name string, typ op.FieldType) bool {
		rf, ok := values[name]
		if !ok || !changed(cmd, name, aliases[name]) {
			return false
		}
		if typ != op.TypeBool {
			return true
		}
		last := ""
		if len(rf.vals) > 0 {
			last = rf.vals[len(rf.vals)-1]
		}
		b, err := strconv.ParseBool(last)
		return err == nil && b
	}
	for _, f := range fields {
		if f.Flag == "" || f.Type != op.TypeBool || slices.Contains(notClearPairs, f.Flag) {
			continue
		}
		for _, p := range clearPrefixes {
			base, ok := strings.CutPrefix(f.Flag, p)
			if !ok {
				continue
			}
			i := slices.IndexFunc(fields, func(b op.FieldInfo) bool { return b.Flag == base })
			if i < 0 || !on(f.Flag, f.Type) || !on(base, fields[i].Type) {
				continue
			}
			keep := "give --" + base + " \"value\" to set it"
			if fields[i].Type == op.TypeBool {
				keep = "give --" + base + " to turn it on"
			}
			return errs.New(UsageCode, "--"+f.Flag+" and --"+base+" cannot be used together").
				WithHint(keep + ", or --" + f.Flag + " alone, not both")
		}
	}
	return nil
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
	if e := badFlagValue(cmd, err); e != nil {
		return printUsage(cmd, cfg, e)
	}
	return printUsage(cmd, cfg, errs.Wrap(UsageCode, err.Error(), err).WithHint(hint))
}

// badFlagValueRe matches pflag's message for a value its own parser
// refused: `invalid argument "maybe" for "--json" flag: strconv.ParseBool:
// parsing "maybe": invalid syntax` (also `-j, --json`).
var badFlagValueRe = regexp.MustCompile(`^invalid argument "(.*)" for "(?:-\w, )?--([\w-]+)" flag: `)

// badFlagValue words a value pflag's own parser refused in plain words,
// by the flag's type, with no Go parser text (devco:L-9: --json=maybe
// printed strconv.ParseBool). nil when err is not such a refusal.
func badFlagValue(cmd *cobra.Command, err error) *errs.Error {
	m := badFlagValueRe.FindStringSubmatch(err.Error())
	if m == nil {
		return nil
	}
	val, name := m[1], m[2]
	want, form := "a valid value", "--"+name+" <value>"
	if fl := cmd.Flags().Lookup(name); fl != nil {
		switch t := fl.Value.Type(); {
		case t == "bool":
			want, form = "true or false", "--"+name+" alone to turn it on, --"+name+"=false to turn it off"
		case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint"):
			want, form = "a whole number", "--"+name+" 10"
		case t == "duration":
			want, form = "a duration like 45m or 1h30m", "--"+name+" 45m"
		case t == "float32" || t == "float64":
			want, form = "a number", "--"+name+" 1.5"
		}
	}
	return errs.New(UsageCode, "--"+name+" takes "+want+", not "+strconv.Quote(val)).
		WithHint("give " + form + "; " + cmd.CommandPath() + " --help lists its flags")
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
	_ = printError(cmd.OutOrStdout(), cmd.ErrOrStderr(), e, JSONRequested(cfg.args), Palette{}, cmd.CommandPath())
	return &exitError{code: ExitUsage, cause: e}
}

// boolWords are the words a person types after a switch meaning to set it
// (`--yes false`, `--live no`). pflag reads a switch with no `=value` as
// on and the word as a positional value; U-4: that is an error naming the
// right form, never a value.
var boolWords = []string{"true", "false", "yes", "no", "on", "off", "1", "0", "t", "f"}

// straySwitchWord refuses a bool word typed right after a switch in the raw
// arguments when the parser kept it as a positional (it is in args). raw is
// the binary's arguments (WithArgs); without them nothing is checked.
func straySwitchWord(cmd *cobra.Command, raw, args []string) *errs.Error {
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] == "--" {
			return nil
		}
		name, ok := strings.CutPrefix(raw[i], "--")
		if !ok || strings.Contains(name, "=") {
			continue
		}
		fl := cmd.Flags().Lookup(name)
		word := raw[i+1]
		if fl == nil || fl.NoOptDefVal == "" || !slices.Contains(boolWords, strings.ToLower(word)) || !slices.Contains(args, word) {
			continue
		}
		off := "leave it out"
		if neg := cmd.Flags().Lookup("no-" + name); neg != nil {
			off = "give --no-" + name
		}
		return errs.New(UsageCode, "--"+name+" is a switch and takes no value ("+strconv.Quote(word)+" after it)").
			WithHint("to turn it on give --" + name + " alone; to turn it off " + off)
	}
	return nil
}

// switchWords maps each positional word typed right after a long switch in
// the raw arguments (`--json extra`) to that switch's name, for the
// decoder's surplus-positional error (op.ArgsDecoder.AfterSwitch,
// devco:L-4). A word the parser did not keep as a positional is left out.
func switchWords(cmd *cobra.Command, raw, args []string) map[string]string {
	var out map[string]string
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] == "--" {
			break
		}
		name, ok := strings.CutPrefix(raw[i], "--")
		if !ok || strings.Contains(name, "=") {
			continue
		}
		fl := cmd.Flags().Lookup(name)
		word := raw[i+1]
		if fl == nil || fl.NoOptDefVal == "" || strings.HasPrefix(word, "-") || !slices.Contains(args, word) {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		if _, seen := out[word]; !seen {
			out[word] = name
		}
	}
	return out
}

// AnnotationArgFlags is the cobra annotation of a generated command that
// names, in argument order, the flag each quiet positional stands for
// (U-4): `id,title...` — "..." marks a rest argument whose words are one
// value, "+" one whose words are one value each (a repeatable flag). The
// docs gate reads it to show a positional's named form.
const AnnotationArgFlags = "lore.surface.cli.argflags"

// argFlags is the AnnotationArgFlags value of fields.
func argFlags(fields []op.FieldInfo) string {
	var args []op.FieldInfo
	for _, f := range fields {
		if f.Arg != op.NoArg {
			args = append(args, f)
		}
	}
	slices.SortFunc(args, func(a, b op.FieldInfo) int { return a.Arg - b.Arg })
	out := make([]string, 0, len(args))
	for _, f := range args {
		name := f.Flag
		if f.ArgOf != "" {
			name = f.ArgOf
		}
		switch {
		case f.Rest && (f.Type == op.TypeStrings || f.Group):
			name += "+"
		case f.Rest:
			name += "..."
		}
		out = append(out, name)
	}
	return strings.Join(out, ",")
}

// helpCommand is `lore help [command]`. Cobra's own help command resolves
// the words with Find, which stops at the deepest command it knows and
// keeps the rest as arguments: `lore help zznosuchword` printed the root
// help and exited 0, so a mistyped topic looked like success
// (run:tests:q-help-topic). This one refuses a word that names no command
// the way the command line itself does (unknownWord: E_INVALID_INPUT, exit
// 20, near spellings in the hint), and prints the help of a known one.
func helpCommand(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long:  "Help provides help for any command in the application.\nSimply type " + RootName + " help [path to command] for full details.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			target, rest, err := c.Root().Find(args)
			if err != nil || target == nil {
				target, rest = c.Root(), args
			}
			if len(rest) > 0 {
				return unknownWord(target, cfg, rest[0])
			}
			target.InitDefaultHelpFlag()
			target.InitDefaultVersionFlag()
			return target.Help()
		},
	}
}
