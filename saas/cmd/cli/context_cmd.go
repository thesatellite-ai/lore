// context_cmd.go — `lore context --internal` (§1 Commands, F20 edge case):
// internal, global and personal rows for tools that cannot follow the
// CLAUDE.local.md import (.cursorrules, Codex). It prints to stdout and
// never writes a file inside the checkout; the "never quote" rule of the
// render applies to whatever the tool does with it.
package main

import (
	"errors"
	"fmt"
	"os"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// Flags of lore context.
const (
	flagContextInternal = "internal"
	flagContextTable    = "table"
	flagContextSearch   = "search"
)

// errContextNeedsInternal: the only context this command prints today.
var errContextNeedsInternal = errors.New("pass --internal (the repo's own context is .lore/LORE.md)")

func newContextCommand() *cobra.Command {
	var internal, asJSON bool
	var table, search string
	cmd := &cobra.Command{
		Use:   "context --internal",
		Short: "Print internal, global and personal context for tools that cannot follow an import",
		Example: `  lore context --internal
  lore context --internal --table rules --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !internal {
				return errcodes.New(errcodes.InvalidInput, errContextNeedsInternal.Error())
			}
			ctx := cmd.Context()
			if asJSON {
				return writeJSONOut(map[string]any{"data": externalJSONRows(ctx, table, search)})
			}
			fmt.Println(renderHeader)
			if n := printExternalRows(ctx, os.Stdout, table, search); n == 0 {
				fmt.Println("(nothing internal or personal loads in this repo)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&internal, flagContextInternal, false, "internal, global and personal rows (outside this checkout)")
	cmd.Flags().StringVar(&table, flagContextTable, "", "only this table (rules, memories, decisions, tasks, …)")
	cmd.Flags().StringVar(&search, flagContextSearch, "", "only rows whose title or body contains this text")
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "machine-readable output")
	return cmd
}
