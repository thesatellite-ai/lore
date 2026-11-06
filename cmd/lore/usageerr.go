package main

// usageerr.go — FLAG2 for the host commands (E-GAPS-1 G1): a wrong
// argument count exits 20 E_INVALID_INPUT with cobra's own text, as the
// generated commands already do (surface/cli usageError) and the old
// binary did (saas/cmd/cli classifyInputErrors).

import (
	"errors"

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
