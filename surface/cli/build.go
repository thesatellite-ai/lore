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
	// The root prints cobra's own errors (unknown command); the generated
	// commands silence theirs because they print them in the lore format.
	root := &cobra.Command{
		Use:          RootName,
		SilenceUsage: true,
	}
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
			parent = group(parent, w)
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

// group returns the noun command name under parent, creating it.
func group(parent *cobra.Command, name string) *cobra.Command {
	if c := child(parent, name); c != nil {
		return c
	}
	g := &cobra.Command{
		Use:           name,
		Short:         strings.TrimSpace(parent.CommandPath()+" "+name) + " commands",
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
			return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
		},
	}
	parent.AddCommand(g)
	return g
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
	for _, f := range fields {
		if f.Flag == "" {
			continue
		}
		rf := &rawFlag{typ: f.Type}
		values[f.Flag] = rf
		desc := f.Desc
		if f.Required {
			desc += requiredSuffix
		}
		fl := c.Flags().VarPF(rf, f.Flag, "", desc)
		if f.Type == op.TypeBool {
			fl.NoOptDefVal = boolNoValue
		}
	}
	asJSON := c.Flags().Bool(FlagJSON, false, "print the result (or the error) as JSON")
	asksFirst := s.Confirm.CanAsk()
	yes := new(bool)
	if asksFirst {
		c.Flags().BoolVar(yes, FlagYes, false, "answer yes to the confirmation without a terminal")
	}
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		// A cobra flag parse error (unknown flag, missing value) is a usage
		// error: exit 1 (CLI_REFERENCE 1.5), printed the way cobra prints.
		_, _ = io.WriteString(cmd.ErrOrStderr(), "Error: "+err.Error()+"\nRun '"+cmd.CommandPath()+" --help' for usage.\n")
		return &exitError{code: ExitUsage, cause: err}
	})
	c.RunE = func(cmd *cobra.Command, args []string) error {
		flags := map[string][]string{}
		for name, rf := range values {
			if cmd.Flags().Changed(name) {
				flags[name] = rf.vals
			}
		}
		res, err := run(cmd, e, runner, cfg, op.ArgsDecoder{Args: args, Flags: flags}, *yes)
		out := cmd.OutOrStdout()
		if err != nil {
			return &exitError{code: printError(out, cmd.ErrOrStderr(), err, *asJSON), cause: err}
		}
		if *asJSON {
			err = printJSON(out, res)
		} else {
			err = printHuman(out, res)
		}
		if err != nil {
			return &exitError{code: printError(out, cmd.ErrOrStderr(), err, *asJSON), cause: err}
		}
		return nil
	}
	return c
}

// run calls the Runner and, for an interactive caller that has not said yes,
// turns E_NEEDS_CONFIRM into a [y/N] prompt and one re-run with Yes set.
//
// Why here: the Runner decides THAT an operation asks; only the surface can
// talk to a person. A non-interactive caller gets the error back unchanged
// (the agent asks its human and re-runs with --yes).
func run(cmd *cobra.Command, e op.Entry, runner op.Runner, cfg config, d op.Decoder, yes bool) (op.Result, error) {
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
	res, err := runner.Run(ctx, e, d, caller)
	if !errs.Has(err, errs.NeedsConfirm) || !caller.Interactive || caller.Yes {
		return res, err
	}
	if !ask(cmd.InOrStdin(), cmd.ErrOrStderr(), errs.EnvelopeOf(err).Error.Message) {
		return op.Result{}, errs.New(errs.NeedsConfirm, notConfirmed)
	}
	caller.Yes = true
	return runner.Run(ctx, e, d, caller)
}

// ask prints question with [y/N] and reads one line.
func ask(in io.Reader, out io.Writer, question string) bool {
	_, _ = io.WriteString(out, question+confirmPrompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return slices.Contains(yesAnswers, strings.ToLower(strings.TrimSpace(line)))
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
		if f.Required {
			parts = append(parts, "<"+f.Name+">")
		} else {
			parts = append(parts, "["+f.Name+"]")
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
		if f.Required {
			b.WriteString(requiredSuffix)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
