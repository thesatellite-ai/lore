package main

// shortcuts.go — the short CLI words of the release binary (CLI_REFERENCE
// 2.7, 2.13, 3.6, 3.8, 3.9), ported from saas/cmd/cli row_shortcut.go and
// sql_shortcut.go so cmd/lore answers the same spellings:
//
//	lore show <id> [--origin] ─► row show
//	lore move <id> --to t     ─► row move (same id, PROV5; the engine's
//	                             write guards apply; no org here, FA-OLDCODE-1)
//	lore lint [--code c]      ─► lint run
//	lore machine(s) [show]    ─► machine list | machine show
//	lore trash [--type …]     ─► trash list
//	lore sql "<query>"        ─► sql run
//	lore plan apply …         ─► refused, hint: lore mission apply (2.15)

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/thesatellite-ai/lore/model/errs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslint "github.com/thesatellite-ai/lore/ops/lint"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	opsql "github.com/thesatellite-ai/lore/ops/sql"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// Verbs the shortcuts run.
const (
	rowShowVerb     = "show"
	rowMoveVerb     = "move"
	lintRunVerb     = "run"
	machineListVerb = "list"
	trashListVerb   = "list"
	sqlRunVerb      = "run"
	planApplyVerb   = "apply"
	missionApply    = "lore mission apply "
)

// mountShortcuts adds the short words; a missing core command adds nothing.
func mountShortcuts(root *cobra.Command) {
	if show := find(root, opsrow.Noun, rowShowVerb); show != nil {
		root.AddCommand(shortcutOf(show, "show <id>", "Show any row by id (--origin: where it came from)"))
	}
	if mv := find(root, opsrow.Noun, rowMoveVerb); mv != nil {
		root.AddCommand(shortcutOf(mv, "move <id>", "Move a row to another store (same id; asks first)"))
	}
	defaultVerb(root, opslint.Noun, lintRunVerb)
	if g := defaultVerb(root, opsinfra.NounMachine, machineListVerb); g != nil {
		g.Aliases = append(g.Aliases, opsinfra.NounMachine+"s")
	}
	defaultVerb(root, opstrash.Noun, trashListVerb)
	defaultVerb(root, opsql.Noun, sqlRunVerb)
	if g := find(root, opsplan.Noun); g != nil {
		g.AddCommand(&cobra.Command{
			Use: planApplyVerb, Hidden: true, Args: cobra.ArbitraryArgs, DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				return errs.New(errs.InvalidInput, `unknown command "`+planApplyVerb+`" for "lore `+opsplan.Noun+`"`).
					WithHint(strings.TrimSpace(missionApply + strings.Join(args, " ")))
			},
		})
	}
}

// find returns the command at path, nil when any word is missing.
func find(root *cobra.Command, path ...string) *cobra.Command {
	c, rest, err := root.Find(path)
	if err != nil || len(rest) != 0 || c.Name() != path[len(path)-1] {
		return nil
	}
	return c
}

// defaultVerb makes bare `lore <noun>` run `lore <noun> <verb>` (other verbs
// stay subcommands) and returns the group; nil when either is missing.
func defaultVerb(root *cobra.Command, noun, verb string) *cobra.Command {
	g := find(root, noun)
	run := find(root, noun, verb)
	if g == nil || run == nil || run.RunE == nil {
		return nil
	}
	g.Short = run.Short
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = forward(run)
	return g
}

// shortcutOf is a top-level command that parses its arguments with target's
// flags and runs target, so both spellings share one implementation.
func shortcutOf(target *cobra.Command, use, short string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Long: target.Long, Example: target.Example,
		DisableFlagParsing: true,
		RunE:               forward(target),
	}
}

// forward runs target with args parsed by target's own flag set.
func forward(target *cobra.Command) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		target.SetContext(cmd.Context())
		target.SetOut(cmd.OutOrStdout())
		target.SetErr(cmd.ErrOrStderr())
		if err := target.ParseFlags(args); err != nil {
			if errors.Is(err, pflag.ErrHelp) {
				return target.Help()
			}
			return target.FlagErrorFunc()(target, err)
		}
		if h, _ := target.Flags().GetBool("help"); h {
			return target.Help()
		}
		rest := target.Flags().Args()
		if err := target.ValidateArgs(rest); err != nil {
			return err
		}
		return target.RunE(target, rest)
	}
}
