// sql_shortcut.go — `lore sql "<query>"` (spec 15 step 5, MQ6): the
// generated `lore sql run` with no verb. The core mounts sql.run as
// `lore sql run …`; this makes the noun itself run it, so both spellings
// work with the same flags (--show-sensitive, --yes, --json). No v0.1.17
// counterpart.
package main

import (
	"github.com/spf13/cobra"

	opsql "github.com/thesatellite-ai/lore/ops/sql"
)

// sqlRunVerb is the generated verb the shortcut runs.
const sqlRunVerb = "run"

// sqlShortcutExample is the group's help example.
const sqlShortcutExample = `  lore sql "SELECT name, ip FROM res_server"
  lore sql run "SELECT name, ip FROM res_server"`

// mountSQLShortcut makes `lore sql <query…>` run `lore sql run <query…>`.
// The group parses no flags itself (its own flags would refuse the run
// verb's), so every argument goes to the run command, which parses its
// flags and runs through the same wrapped RunE as `lore sql run`.
func mountSQLShortcut(root *cobra.Command) {
	g, _, err := root.Find([]string{opsql.Noun})
	if err != nil || g.Name() != opsql.Noun {
		return
	}
	run, _, err := g.Find([]string{sqlRunVerb})
	if err != nil || run == g || run.RunE == nil {
		return
	}
	g.Example = sqlShortcutExample
	// CLI_REFERENCE 3.6: the shortcut takes the run verb's flags; listing
	// the same flag objects on the group makes `lore sql --help` show them
	// (parsing still happens in run, below).
	g.Flags().AddFlagSet(run.LocalFlags())
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
			return cmd.Help()
		}
		if err := run.ParseFlags(args); err != nil {
			return run.FlagErrorFunc()(run, err)
		}
		rest := run.Flags().Args()
		if err := run.ValidateArgs(rest); err != nil {
			return err
		}
		run.SetContext(cmd.Context())
		return run.RunE(run, rest)
	}
}
