package app

// envdb.go — LORE_DB is refused like --db (E-GAPS-1 S1).

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// envLoreDB is v0.1.17's database path variable. 0.1.18 rows are files read
// from the repository, so a set LORE_DB is refused like --db
// (docs/reference/environment.md, flags.md), never silently ignored: a
// script that still points at a database must learn it is not read.
const envLoreDB = "LORE_DB"

// refuseEnvDB makes every command under root that has its own --db flag
// (each one refuses that flag: the compat flags of the row commands and the
// retired `project shared`) refuse a non-empty LORE_DB before it runs, with
// E_UNSUPPORTED (exit 23) and the --db refusal's hint. Commands without
// --db (version, init, render, ...) never read the variable.
func refuseEnvDB(root *cobra.Command) {
	for _, sub := range root.Commands() {
		refuseEnvDB(sub)
	}
	if root.NonInheritedFlags().Lookup(hosthooks.FlagDB) == nil {
		return
	}
	inner := root.PreRunE
	root.PreRunE = func(cmd *cobra.Command, args []string) error {
		if os.Getenv(envLoreDB) != "" {
			return errs.New(errs.Unsupported, envLoreDB+" is not supported by this command: its rows are row files read from the repository, not lore.db").
				WithHint("unset " + envLoreDB + " and run the command inside the repository (or name a store with --store / --to)")
		}
		if inner != nil {
			return inner(cmd, args)
		}
		return nil
	}
}
