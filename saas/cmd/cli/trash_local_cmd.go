// trash_local_cmd.go — the v0.1.17 trash forms under the `trash` noun
// (CLI_REFERENCE 3.9, DELETION 501-502, DQ1: one word, one meaning).
//
// The trash noun is generated from ops/trash (the row trash of every
// store). Three forms read lore.db, which only this binary opens, so they
// are added here to the generated commands:
//
//	lore trash --local [--json]          ─► sync trash list (this machine's _lore_sync_trash)
//	lore trash restore --local <n>       ─► sync trash restore <n>
//	lore trash purge --archived-before <date|age> --confirm|--dry-run
//	                                     ─► sync purge --archived-before
//
// Each wrapper runs the old command with the flags it got; without the
// flag the generated command runs unchanged.
package main

import (
	"fmt"
	"os"

	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// localTrashNote heads `lore trash --local` (DELETION 501).
const localTrashNote = "this machine only: local safety copies of rows sync removed (never shared)"

// Flags this file adds to the generated trash commands.
const (
	flagTrashLocal = "local"
	trashVerbList  = "list"
	trashVerbRest  = "restore"
	trashVerbPurge = "purge"
)

// attachTrashLocal adds --local (list, restore) and --archived-before
// (purge) to the generated trash commands. A missing command (the core not
// mounted) is skipped.
func attachTrashLocal(root *cobra.Command) {
	g, _, err := root.Find([]string{trash.Noun})
	if err != nil || g.Name() != trash.Noun {
		return
	}
	for _, sub := range g.Commands() {
		switch sub.Name() {
		case trashVerbList:
			wrapTrashLocal(sub, "this machine's safety copies (_lore_sync_trash), this machine only; replaces lore sync trash",
				func(c *cobra.Command, _ []string) []string {
					if j, _ := c.Flags().GetBool(constants.FlagJSON); j {
						return []string{"--" + constants.FlagJSON}
					}
					return nil
				}, newSyncTrashCommand)
		case trashVerbRest:
			wrapTrashLocal(sub, "restore this machine's safety copy <n> (lore trash --local lists them); replaces lore sync trash restore",
				func(_ *cobra.Command, args []string) []string { return append([]string{trashVerbRest}, args...) },
				newSyncTrashCommand)
		case trashVerbPurge:
			wrapArchivedPurge(sub)
		}
	}
}

// wrapTrashLocal adds --local to c: with it, c runs the old command built
// by build with the arguments argv returns, instead of the generated op.
func wrapTrashLocal(c *cobra.Command, usage string, argv func(*cobra.Command, []string) []string, build func() *cobra.Command) {
	c.Flags().Bool(flagTrashLocal, false, usage)
	inner, innerArgs := c.RunE, c.Args
	c.Args = func(cmd *cobra.Command, args []string) error {
		if local, _ := cmd.Flags().GetBool(flagTrashLocal); local {
			return nil // the old command checks its own arguments
		}
		if innerArgs == nil {
			return nil
		}
		return innerArgs(cmd, args)
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if local, _ := cmd.Flags().GetBool(flagTrashLocal); !local {
			return inner(cmd, args)
		}
		a := argv(cmd, args)
		if len(a) == 0 {
			// DELETION 501: the copies are marked "this machine only".
			fmt.Fprintln(os.Stderr, style.Muted(localTrashNote))
		}
		return runOld(cmd, build(), a)
	}
}

// wrapArchivedPurge adds --archived-before to the generated purge: the
// v0.1.17 `lore sync purge --archived-before` under the trash noun (3.9),
// with the purge's own --confirm and --dry-run.
func wrapArchivedPurge(c *cobra.Command) {
	var before string
	c.Flags().StringVar(&before, constants.FlagArchivedBefore, "", "purge v0.1.17 archived rows archived before a date (2006-01-02) or age (90d); with --confirm or --dry-run")
	inner, innerArgs := c.RunE, c.Args
	c.Args = func(cmd *cobra.Command, args []string) error {
		if before != "" || innerArgs == nil {
			return nil
		}
		return innerArgs(cmd, args)
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if before == "" {
			return inner(cmd, args)
		}
		argv := []string{"--" + constants.FlagArchivedBefore, before}
		confirmed := false
		for _, f := range []string{constants.FlagConfirm, constants.FlagDryRun} {
			if on, _ := cmd.Flags().GetBool(f); on {
				argv = append(argv, "--"+f)
				confirmed = true
			}
		}
		if !confirmed {
			// DQ2: every purge needs the literal --confirm, named in next.
			q := "purge every v0.1.17 row archived before " + before + " for good"
			return errs.New(errs.NeedsConfirm, "trash: "+q).
				WithHint("ask the person, then re-run with --confirm (or --dry-run to preview)").
				WithQuestion(q+"?").
				WithNext("lore", "trash", "purge", "--"+constants.FlagArchivedBefore, before, "--"+constants.FlagConfirm)
		}
		argv = append(argv, args...)
		return runOld(cmd, newSyncPurgeCommand(), argv)
	}
}

// runOld runs the old command old with argv through its own flag set and
// argument check (forward, row_shortcut.go).
func runOld(cmd, old *cobra.Command, argv []string) error {
	target := old
	if len(argv) > 0 && argv[0] == trashVerbRest {
		for _, sub := range old.Commands() {
			if sub.Name() == trashVerbRest {
				target, argv = sub, argv[1:]
			}
		}
	}
	return forward(target)(cmd, argv)
}
