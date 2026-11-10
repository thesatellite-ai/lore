package app

import (
	"github.com/spf13/cobra"

	opsql "github.com/thesatellite-ai/lore/ops/sql"
)

// sqlShortcutExample is the Example of the bare `lore sql` group, v0.1.17's
// text (saas/cmd/cli sql_shortcut.go): the shortcut and its long form.
const sqlShortcutExample = `  lore sql "SELECT name, ip FROM res_server"
  lore sql run "SELECT name, ip FROM res_server"`

// sqlSourcesHelp explains the `source` column of the <table>_all views,
// including the one case where it differs from lists and search: two
// stores.yaml entries on one place keep both names here (decision
// 2026-11-08; wire markShared).
const sqlSourcesHelp = `

Sources:
  Each <table>_all view (memories_all, rules_all, res__all, ...) has a
  leading "source" column: the store's name in .lore/stores.yaml, or repo.
  The plain table name (memories) reads the nearest copy.
  Two entries on one place (same url, branch and folder): lists and search
  show its rows once under the first entry's name; lore sql keeps both names
  as sources, so a query over both sees each row twice.`

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
	g.Long += sqlSourcesHelp
	if run := find(g, sqlRunVerb); run != nil {
		run.Long += sqlSourcesHelp
	}
	forwardRun := g.RunE
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return forwardRun(cmd, args)
	}
}
