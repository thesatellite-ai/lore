// entity_commands.go — the `lore actor` group of the old binary.
//
// Its other v0.1.17 groups are gone: plugin list|trust|untrust and
// mount-alias list are dropped in 0.1.18 (p3/dropped.md), pii-pattern and
// task view moved to core operations (ops/piipattern, ops/task).
package main

import "github.com/spf13/cobra"

// newActorCommand is the `lore actor` group. Every verb is a core
// operation (ops/infra: list, show, register, deactivate, delete); a pure
// group, so cli.Mount reuses it.
func newActorCommand() *cobra.Command {
	return &cobra.Command{Use: "actor", Short: "Inspect actors (humans, agents, hooks, plugins)"}
}
