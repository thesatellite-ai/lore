// generated_commands.go — registers the v0.1.17 "generated" entity command
// groups that still run on lore.db (session, querylog, renderhistory;
// each defined in its own file).
//
// Moved to the 0.1.18 core and mounted by core_mount.go: pattern, playbook,
// prompt (ops/pattern, ops/playbook, ops/prompt), reminder (ops/reminder),
// and architecturenote, behaviour, cookbookrecipe, incident, suggestion,
// tastepref, workflow, workspace and handoff (ops/<noun>, phase 5 group 6),
// and run (ops/run on the machine store, P3-LOCAL-3).
package main

import "github.com/spf13/cobra"

// registerExtraCommands attaches the generated entity groups still served by
// lore.db to root.
func registerExtraCommands(root *cobra.Command) {
	root.AddCommand(newSessionGroup())
	root.AddCommand(newQueryLogGroup())
	root.AddCommand(newRenderHistoryGroup())
}
