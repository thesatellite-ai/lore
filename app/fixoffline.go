package app

import (
	"os"
	"slices"

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
func offlineFixDryRun(args []string) {
	if commandName(args) != opsfix.Noun || !slices.ContainsFunc(args, func(a string) bool { return a == fixDryRunFlag || a == fixDryRunFlag+"=true" }) {
		return
	}
	_ = os.Setenv(gitenv.EnvOffline, gitenv.On) // one command per process: the CLI entry point
	gitbackend.ForbidClones()                   // B4-14: no store clone either
	machine.SkipCacheWrites()                   // … and no machine.json
}
