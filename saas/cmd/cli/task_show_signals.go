// task_show_signals.go — `lore task show <id> --signals` (LORE_TEAM_SPEC F7
// Debug, PENDING-60): after the task, the claim on it as this clone sees it
// now (holder, session, token prefix, epoch, expires, renewed_seq, work
// branch, owned resources), read from the repo's signals ref.
//
// v0.1.17 printed it at the end of its own `task show` (task.go +
// task_list_team.go printTaskSignals, 4e6ed1c). The 0.1.18 `task show` is
// the core's generated command, and the signals machinery lives in this
// binary (the core cannot import it), so the flag is added here, on the
// mounted command, and the claim is printed after the core's own output.
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/surface/cli"
)

// flagSignals is `task show --signals`.
const flagSignals = "signals"

// withTaskShowSignals adds --signals to the core-mounted `lore task show`
// under root (core_mount.go calls it after cli.Mount). A root without the
// command (a build without the task noun) is left as it is.
func withTaskShowSignals(root *cobra.Command) {
	c, _, err := root.Find([]string{opstask.Noun, "show"})
	if err != nil || c.Name() != "show" || c.RunE == nil || c.Flags().Lookup(flagSignals) != nil {
		return
	}
	c.Flags().Bool(flagSignals, false, "show the claim as seen now (holder, token prefix, expires, renewed_seq)")
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := run(cmd, args); err != nil {
			return err
		}
		want, _ := cmd.Flags().GetBool(flagSignals)
		// v0.1.17: --json prints the row only; the claim is a text view.
		asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON)
		if !want || asJSON || len(args) == 0 {
			return nil
		}
		return printTaskSignals(cmd, args[0])
	}
}

// printTaskSignals prints the claim on task id from this repo's own
// signals ref, as v0.1.17 did: a client repo is refused (its claims live
// on the linked store's ref, D8) and an unknown link status fails closed.
func printTaskSignals(cmd *cobra.Command, id string) error {
	ctx := cmd.Context()
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return err
	}
	text, err := claimText(ctx, st, w.Folder, id)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}
