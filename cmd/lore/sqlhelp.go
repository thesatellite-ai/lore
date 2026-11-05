package main

import (
	"github.com/spf13/cobra"

	opsql "github.com/thesatellite-ai/lore/ops/sql"
)

// sqlShortcutExample is the Example of the bare `lore sql` group, v0.1.17's
// text (saas/cmd/cli sql_shortcut.go): the shortcut and its long form.
const sqlShortcutExample = `  lore sql "SELECT name, ip FROM res_server"
  lore sql run "SELECT name, ip FROM res_server"`

// mountSQLHelp makes a bare `lore sql` print the group help with the
// shortcut example and exit 0, as v0.1.17 did, instead of forwarding an
// empty query to sql.run (E_INVALID_INPUT "missing required field query").
// It runs after mountShortcuts, which made `lore sql <query>` forward to
// `lore sql run`; every other form still forwards.
func mountSQLHelp(root *cobra.Command) {
	g := find(root, opsql.Noun)
	if g == nil || g.RunE == nil {
		return
	}
	g.Example = sqlShortcutExample
	forwardRun := g.RunE
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return forwardRun(cmd, args)
	}
}
