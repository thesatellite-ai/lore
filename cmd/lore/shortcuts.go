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
//	lore sync / lore land     ─► sync run / land run
//	lore commit-show <sha>    ─► commit show (hidden, v0.1.17 spelling)
//	lore upgrade [--check …]  ─► upgrade run
//	lore plan apply …         ─► refused, hint: lore mission apply (2.15)

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opslint "github.com/thesatellite-ai/lore/ops/lint"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opsselfupgrade "github.com/thesatellite-ai/lore/ops/selfupgrade"
	opsql "github.com/thesatellite-ai/lore/ops/sql"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
)

// Verbs the shortcuts run.
const (
	rowShowVerb     = "show"
	rowMoveVerb     = "move"
	commitShowVerb  = "show"
	commitShowAlias = "commit-show"
	lintRunVerb     = "run"
	machineListVerb = "list"
	trashListVerb   = "list"
	sqlRunVerb      = "run"
	searchRunVerb   = "run"
	planApplyVerb   = "apply"
	missionApply    = "lore mission apply "
	helpFlag        = "help"
)

// mountShortcuts adds the short words; a missing core command adds nothing.
func mountShortcuts(root *cobra.Command) {
	if show := find(root, opsrow.Noun, rowShowVerb); show != nil {
		root.AddCommand(shortcutOf(show, "show <id>", "Show any row by id (--origin: where it came from)"))
	}
	if show := find(root, opslink.CommitNoun, commitShowVerb); show != nil {
		// v0.1.17's spelling of `lore commit show <sha>` (CLI 2.15, D33).
		alias := shortcutOf(show, commitShowAlias+" <sha>", "v0.1.17 spelling of `lore commit show`")
		alias.Hidden = true
		root.AddCommand(alias)
	}
	if mv := find(root, opsrow.Noun, rowMoveVerb); mv != nil {
		root.AddCommand(shortcutOf(mv, "move <id>", "Move a row to another store (same id; asks first)"))
	}
	defaultVerb(root, opslint.Noun, lintRunVerb)
	defaultVerb(root, opssync.Noun, opssync.VerbRun)        // lore sync ─► sync run
	defaultVerb(root, opssync.NounLand, opssync.VerbRun)    // lore land ─► land run
	defaultVerb(root, opssync.NounExplain, opssync.VerbRun) // lore explain <id> ─► explain run (v0.1.17 spelling)
	if g := defaultVerb(root, opsinfra.NounMachine, machineListVerb); g != nil {
		g.Aliases = append(g.Aliases, opsinfra.NounMachine+"s")
	}
	defaultVerb(root, opstrash.Noun, trashListVerb)
	defaultVerbAt(root, []string{opssync.Noun, opssync.NounTrash}, trashListVerb) // bare `lore sync trash` lists (DELETION_DESIGN 3.10)
	defaultVerb(root, opsql.Noun, sqlRunVerb)
	defaultVerb(root, opswhoami.Noun, opswhoami.ShowVerb)          // CLI_REFERENCE 3.8 (P3-ORG-G-3)
	defaultVerb(root, opsselfupgrade.Noun, opsselfupgrade.RunVerb) // CLI_REFERENCE 3.5 (P3-UPG-1)
	defaultVerb(root, opssearch.Noun, searchRunVerb)               // CLI_REFERENCE 2.5 (P3-MISC-1)
	defaultVerb(root, "why-context", "show")                       // CLI_REFERENCE 2.14 (P3-MISC-3)
	defaultVerb(root, "setup", "run")                              // CLI_REFERENCE 2.14, 3.18 (P3-MISC-4)
	defaultVerb(root, opscoord.NounInbox, opscoord.InboxVerb)      // bare `lore inbox` (CLI_REFERENCE 2.11, P3-COORD-1)
	defaultVerb(root, "support-bundle", "run")                     // CLI_REFERENCE 2.11 (P3-MISC-5)
	defaultVerb(root, "context", "fetch")                          // CLI_REFERENCE 2.14 (P3-MISC-6)
	defaultVerb(root, "render", "run")                             // CLI_REFERENCE 2.14 (P3-MISC-7)
	defaultVerb(root, "doctor", "run")                             // CLI_REFERENCE 3.7 (P3-MISC-8)
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
	return defaultVerbAt(root, []string{noun}, verb)
}

// defaultVerbAt is defaultVerb for the group at path (a nested group such
// as `lore sync trash`).
func defaultVerbAt(root *cobra.Command, path []string, verb string) *cobra.Command {
	noun := path[len(path)-1]
	g := find(root, path...)
	run := find(root, append(append([]string{}, path...), verb)...)
	if g == nil || run == nil || run.RunE == nil {
		return nil
	}
	g.Short = run.Short
	// "lore explain <id>", "lore sql <query>": the verb's positionals on
	// the bare spelling (cobra names the command by Use's first word).
	g.Use = noun + strings.TrimPrefix(run.Use, run.Name())
	g.Long, g.Example = run.Long, run.Example
	showFlags(g, run)
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = forward(run)
	return g
}

// shortcutOf is a top-level command that parses its arguments with target's
// flags and runs target, so both spellings share one implementation.
func shortcutOf(target *cobra.Command, use, short string) *cobra.Command {
	c := &cobra.Command{
		Use: use, Short: short, Long: target.Long, Example: target.Example,
		DisableFlagParsing: true,
		RunE:               forward(target),
	}
	showFlags(c, target)
	return c
}

// showFlags lists target's flags in c's --help. c never parses them
// (DisableFlagParsing: forward parses with target's own set), so the shared
// *pflag.Flag values are only read for the help text. This is what makes
// `lore move --help` print "lore move <id> [flags]" and bare-noun help
// print "lore whoami [flags]" (CLI_REFERENCE 2.7, 3.8) instead of the
// registered path of the operation they run (lore row move, lore whoami show).
func showFlags(c, target *cobra.Command) {
	target.InitDefaultHelpFlag()
	target.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name != helpFlag && c.Flags().Lookup(f.Name) == nil {
			c.Flags().AddFlag(f)
		}
	})
}

// forward runs target with args parsed by target's own flag set.
func forward(target *cobra.Command) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		target.SetContext(cmd.Context())
		target.SetOut(cmd.OutOrStdout())
		target.SetErr(cmd.ErrOrStderr())
		// Help is cmd's: the spelling the user typed, with target's flags
		// (showFlags), not target's registered path.
		if err := target.ParseFlags(args); err != nil {
			if errors.Is(err, pflag.ErrHelp) {
				return cmd.Help()
			}
			return target.FlagErrorFunc()(target, err)
		}
		if h, _ := target.Flags().GetBool(helpFlag); h {
			return cmd.Help()
		}
		rest := target.Flags().Args()
		if err := target.ValidateArgs(rest); err != nil {
			return err
		}
		return target.RunE(target, rest)
	}
}
