package app

import (
	"os"
	"slices"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
)

// fixDryRunFlag is lore fix's --dry-run on the command line.
const fixDryRunFlag = "--dry-run"

// offlineFixDryRun turns the network off for `lore fix --dry-run` (and
// --all --dry-run), before anything opens a store (owner bug 14): a dry
// run reads what is on disk, so no store is fetched, pushed, cloned or
// asked for its branches (gitenv.EnvOffline). Set on the process, so the
// --all children it starts inherit it too.
// The returned func lifts the no-clone and no-machine.json switches when the
// command ends (tests run many commands in one process).
func offlineFixDryRun(args []string) func() {
	if commandName(args) != opsfix.Noun || !slices.ContainsFunc(args, func(a string) bool { return a == fixDryRunFlag || a == fixDryRunFlag+"=true" }) {
		return func() {}
	}
	_ = os.Setenv(gitenv.EnvOffline, gitenv.On) // one command per process: the CLI entry point
	if testing.Testing() {
		// a test binary runs many commands at once in one process: the
		// process-wide switches below would reach its other commands. The
		// dry run's own ctx still carries git.WithNoClone (host.Runner).
		return func() {}
	}
	clones := gitbackend.ForbidClones() // B4-14: no store clone either
	cache := machine.SkipCacheWrites()  // … and no machine.json
	return func() { clones(); cache() }
}
