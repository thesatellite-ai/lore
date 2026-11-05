// misc_shortcuts.go — the bare spellings of operations whose noun is the
// whole command in the design: `lore why-context [--cut]` runs the
// generated `lore why-context show` (CLI_REFERENCE 2.14, P3-MISC-3). The
// group keeps its subcommands (cobra resolves a subcommand name before the
// group's RunE); everything else is parsed by the verb's own flags.
package main

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	contextop "github.com/thesatellite-ai/lore/ops/context"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opsselfupgrade "github.com/thesatellite-ai/lore/ops/selfupgrade"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// bareVerbs maps a generated noun to the verb its bare spelling runs.
var bareVerbs = []struct{ noun, verb string }{
	{opswhycontext.Noun, "show"},
	{opssetup.Noun, opssetup.RunVerb},
	{opssupportbundle.Noun, opssupportbundle.RunVerb},
	{contextop.Noun, contextop.FetchVerb},
	{opsselfupgrade.Noun, opsselfupgrade.RunVerb}, // CLI_REFERENCE 3.5 (P3-UPG-1)
	{opscoord.NounInbox, opscoord.InboxVerb},      // bare `lore inbox [--since …]` (CLI_REFERENCE 2.11, P3-COORD-1)
	{opsrender.Noun, opsrender.RunVerb},
	{opsdoctor.Noun, opsdoctor.RunVerb},
}

// mountBareVerbs mounts every bareVerbs entry whose group is mounted.
func mountBareVerbs(root *cobra.Command) {
	for _, b := range bareVerbs {
		mountBareVerb(root, cliName(b.noun), b.verb)
	}
}

// cliName is a noun's CLI spelling (snake_case → kebab-case, as op.CLIPath).
func cliName(noun string) string { return strings.ReplaceAll(noun, "_", "-") }

// mountBareVerb makes `lore <noun> [flags]` run `lore <noun> <verb>
// [flags]`; -h/--help prints the verb's help under the bare name.
func mountBareVerb(root *cobra.Command, noun, verb string) {
	g, _, err := root.Find([]string{noun})
	if err != nil || g.Name() != noun {
		return
	}
	run, _, err := g.Find([]string{verb})
	if err != nil || run == g || run.RunE == nil {
		return
	}
	g.Short = run.Short
	g.Example = run.Example
	g.Flags().AddFlagSet(run.LocalFlags())
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if err := run.ParseFlags(args); err != nil {
			if errors.Is(err, pflag.ErrHelp) {
				return cmd.Help()
			}
			return run.FlagErrorFunc()(run, err)
		}
		if h, _ := run.Flags().GetBool("help"); h {
			return cmd.Help()
		}
		rest := run.Flags().Args()
		if err := run.ValidateArgs(rest); err != nil {
			return err
		}
		run.SetContext(cmd.Context())
		return run.RunE(run, rest)
	}
}

// asHostError converts *err to a core error (asCoreError) on the way out
// of a host method behind an operation, so the core surface prints the
// v0.1.17 code and hint instead of E_INTERNAL. Deferred first, it runs
// after every other deferred close.
func asHostError(err *error) {
	if *err != nil {
		*err = asCoreError(*err)
	}
}
