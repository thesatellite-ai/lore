// search_shortcut.go — `lore search "<query>"` (CLI_REFERENCE 2.5): the
// generated `lore search run` with no verb, as v0.1.17 spelled it. The
// core mounts search.run|status|rebuild (ops/search, P3-MISC-1); this makes
// the noun itself run search.run, while `lore search status|rebuild` stay
// subcommands (cobra resolves a subcommand name before the group's RunE).
package main

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// searchRunVerb is the generated verb the shortcut runs.
const searchRunVerb = "run"

// mountSearchShortcut makes `lore search <query…>` run `lore search run
// <query…>` with the run verb's flags; a bare `lore search` prints help.
func mountSearchShortcut(root *cobra.Command) {
	g, _, err := root.Find([]string{opssearch.Noun})
	if err != nil || g.Name() != opssearch.Noun {
		return
	}
	run, _, err := g.Find([]string{searchRunVerb})
	if err != nil || run == g || run.RunE == nil {
		return
	}
	g.Use = opssearch.Noun + " <query>"
	g.Short = run.Short
	g.Example = `  lore search "invoice rounding"
  lore search deploy --tables rule,memory --json
  lore search status`
	g.Flags().AddFlagSet(run.LocalFlags())
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
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
