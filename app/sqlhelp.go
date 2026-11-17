package app

import (
	"github.com/spf13/cobra"

	opsql "github.com/thesatellite-ai/lore/ops/sql"
)

// sqlShortcutExample is the Example of the bare `lore sql` group, v0.1.17's
// text (saas/cmd/cli sql_shortcut.go): the shortcut and its long form.
const sqlShortcutExample = `  lore sql --query "SELECT name, ip FROM res_server"
  lore sql run --query "SELECT name, ip FROM res_server"`

// sqlSourcesHelp explains the `source` and `place` columns of the
// <table>_all views: source is the attached name queries filter on, place
// the label search and lists print (devco D-568); two names on one place
// read it once, under the first (devco D-443, wire onePerPlace).
const sqlSourcesHelp = `

Sources:
  Each <table>_all view (memories_all, rules_all, ...) has a leading
  "source" column: the store's name as attached (.lore/stores.yaml, the
  org's entry, or repo), the name WHERE source = '...' filters on. Next
  comes "place": the label search and the lists print for that store
  (acme/web for a store attached at folder web as acme).
  The plain table name (memories) reads the nearest copy.
  Two entries on one place (same url, branch and folder) are read once,
  under the first entry's name, as lists and search do.`

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
