package app

// migratehooks.go — the context of `lore migrate run|rollback|unlock`, as
// the old binary's withMigrateTeamLock gives it: the team lock on the
// repo's signals ref (wire App.MigrateTeamLock, FORMAT_EVOLUTION 7.2) and,
// in a test build only, the crash and pause points (7.8, wire/host
// MigrateTestHooks; a release build installs none).

import (
	"github.com/spf13/cobra"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// migrateNoun and migrateHookVerbs are the commands that reach the points.
const migrateNoun = "migrate"

var migrateHookVerbs = []string{"run", "rollback", "unlock"}

// withMigrateContext wraps each command's RunE so its context carries the
// team lock and the test hooks (none in a release build).
func withMigrateContext(root *cobra.Command, app *wire.App) {
	for _, verb := range migrateHookVerbs {
		c := find(root, migrateNoun, verb)
		if c == nil || c.RunE == nil {
			continue
		}
		run := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(hosthooks.MigrateTestHooks(opsmigrate.WithTeamLock(cmd.Context(), app.MigrateTeamLock())))
			return run(cmd, args)
		}
	}
}
