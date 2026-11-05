package main

// rootflags.go — the global flags of CLI_REFERENCE 1.1 that the root
// command carries itself (the generated commands carry only their own):
// --trace. -v/--version comes from cobra (root.Version). One function, so
// run and the docs drift tree (docs_drift_test.go) build the same root.

import (
	"github.com/spf13/cobra"

	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// addRootFlags registers the root's global flags; trace receives --trace
// (read per git call by hosthooks.TraceGit).
func addRootFlags(root *cobra.Command, trace *bool) {
	root.PersistentFlags().BoolVar(trace, hosthooks.FlagTrace, false, "print every git command lore runs for live rows, with timing (same as LORE_TRACE=1)")
}
