package app

// usageerr.go — FLAG2 for the host commands (E-GAPS-1 G1): a wrong
// argument count exits 20 E_INVALID_INPUT with cobra's own text, as the
// generated commands already do (surface/cli usageError) and the old
// binary did (saas/cmd/cli classifyInputErrors).

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/surface/cli"
)

// classifyUsageErrors wraps the Args validator of every non-generated
// command under root so a refusal carries E_INVALID_INPUT. Without it the
// plain cobra error reached printHostError as E_INTERNAL (exit 1, text
// lost). run calls it after the last AddCommand, so every host command
// (api, spec, version, init, tui, merge-driver, sync hook, task hook, ...)
// is covered; a validator that already names a code keeps it.
func classifyUsageErrors(root *cobra.Command) {
	for _, sub := range root.Commands() {
		classifyUsageErrors(sub)
	}
	refuseUnknownSubcommand(root)
	if cli.Generated(root) || root.Args == nil {
		return
	}
	inner := root.Args
	root.Args = func(cmd *cobra.Command, args []string) error {
		err := inner(cmd, args)
		var coded *errs.Error
		if err == nil || errors.As(err, &coded) {
			return err
		}
		return errs.New(errs.InvalidInput, err.Error())
	}
}

// refuseUnknownSubcommand makes a host command group (completion, spec,
// api token: a command with subcommands and no Run of its own) refuse a
// word that names none of its subcommands with E_INVALID_INPUT, exit 20,
// as the generated groups do (surface/cli group). Without it cobra prints
// the group's help and exits 0, so a mistyped `lore spec doc` looked like
// success to a script. Bare, the group still prints its help.
func refuseUnknownSubcommand(c *cobra.Command) {
	if cli.Generated(c) || c.Runnable() || !c.HasSubCommands() {
		return
	}
	c.Args = cobra.ArbitraryArgs
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return errs.New(errs.InvalidInput, fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()))
	}
}
