// trash_local_cmd.go — the v0.1.17 trash forms under the `trash` noun
// (CLI_REFERENCE 3.9, DELETION 501-502, DQ1: one word, one meaning).
//
// The trash noun is generated from ops/trash (the row trash of every
// store). Two forms read lore.db, which only this binary opens, so they
// are added here to the generated commands:
//
//	lore trash list --local [--json]     ─► sync trash list (this machine's _lore_sync_trash)
//	lore trash restore --local <n>       ─► sync trash restore <n>
//	lore trash purge --local --purged    ─► lsync.DropPurgedCopies (OV31: drop
//	    [--dry-run|--confirm]                this machine's copies of rows
//	                                         purged elsewhere, DELETION 3.10)
//
// `lore trash purge --archived-before` is the generated op itself
// (ops/trash, DEL-ARCH: it works on the row files, not lore.db).
//
// Each wrapper runs the old command with the flags it got; without the
// flag the generated command runs unchanged.
package main

import (
	"fmt"
	"os"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// localTrashNote heads `lore trash list --local` (DELETION 501).
const localTrashNote = "this machine only: local safety copies of rows sync removed (never shared)"

// Flags this file adds to the generated trash commands.
const (
	flagTrashLocal  = "local"
	flagTrashPurged = "purged"
	trashVerbList   = "list"
	trashVerbRest   = "restore"
	trashVerbPurge  = "purge"
	// jsonKindTrashLocalPurged is the --json kind of purge --local --purged.
	jsonKindTrashLocalPurged = "trash.local_purged"
)

// attachTrashLocal adds --local (list, restore) to the generated trash
// commands. A missing command (the core not
// mounted) is skipped.
func attachTrashLocal(root *cobra.Command) {
	g, _, err := root.Find([]string{trash.Noun})
	if err != nil || g.Name() != trash.Noun {
		return
	}
	for _, sub := range g.Commands() {
		if sub.Name() != trashVerbPurge && sub.Flags().Lookup(flagTrashLocal) != nil {
			continue // P3-TRASHLOCAL-1: the op serves list/restore --local itself (ops/trash/local.go)
		}
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
			wrapTrashLocal(sub, "restore this machine's safety copy <n> (lore trash list --local lists them); replaces lore sync trash restore",
				func(_ *cobra.Command, args []string) []string { return append([]string{trashVerbRest}, args...) },
				newSyncTrashCommand)
		case trashVerbPurge:
			wrapTrashLocalPurge(sub)
		}
	}
}

// wrapTrashLocalPurge adds --local and --purged to the generated purge
// command: `purge --local --purged` drops this machine's safety copies of
// rows purged elsewhere (DELETION 3.10, OV31); every other purge form runs
// the generated op. Like every permanent removal it needs --confirm, or
// --dry-run to only list (DQ2).
func wrapTrashLocalPurge(c *cobra.Command) {
	c.Flags().Bool(flagTrashLocal, false, "with --purged: this machine's safety copies (_lore_sync_trash) only")
	c.Flags().Bool(flagTrashPurged, false, "with --local: drop local copies of rows purged elsewhere")
	inner, innerArgs := c.RunE, c.Args
	isLocal := func(cmd *cobra.Command) (bool, bool) {
		local, _ := cmd.Flags().GetBool(flagTrashLocal)
		purged, _ := cmd.Flags().GetBool(flagTrashPurged)
		return local, purged
	}
	c.Args = func(cmd *cobra.Command, args []string) error {
		if local, _ := isLocal(cmd); local {
			return cobra.NoArgs(cmd, args)
		}
		if innerArgs == nil {
			return nil
		}
		return innerArgs(cmd, args)
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		local, purged := isLocal(cmd)
		if !local && !purged {
			return inner(cmd, args)
		}
		if !local || !purged {
			return errs.New(errs.InvalidInput, "trash purge: --local and --purged go together (lore trash purge --local --purged --confirm)")
		}
		return runLocalPurged(cmd)
	}
}

// runLocalPurged is `lore trash purge --local --purged`: list, then (with
// --confirm) drop, this machine's copies of rows _purged.json lists.
func runLocalPurged(cmd *cobra.Command) error {
	dry, _ := cmd.Flags().GetBool(constants.FlagDryRun)
	confirm, _ := cmd.Flags().GetBool(constants.FlagConfirm)
	asJSON, _ := cmd.Flags().GetBool(constants.FlagJSON)
	t, err := openSyncTarget(&commonFlags{})
	if err != nil {
		return err
	}
	defer t.close()
	ctx := cmd.Context()
	if !dry && !confirm {
		// DQ2: a permanent removal asks for --confirm; list what it would drop.
		found, dropErr := lsync.DropPurgedCopies(ctx, t.db, t.dataDir, true)
		if dropErr != nil {
			return errcodes.New(errcodes.Internal, "list local copies of purged rows").WithCause(dropErr)
		}
		if len(found) == 0 {
			fmt.Println(style.Muted("· no local copies of purged rows"))
			return nil
		}
		msg := fmt.Sprintf("drop %d local cop(ies) of purged rows on this machine for good", len(found))
		return errs.New(errs.NeedsConfirm, "trash purge --local --purged: "+msg).
			WithHint("ask the person, then re-run with --confirm").
			WithQuestion(msg+"?").
			WithNext("lore", "trash", "purge", "--local", "--purged", "--confirm")
	}
	dropped, err := lsync.DropPurgedCopies(ctx, t.db, t.dataDir, dry)
	if err != nil {
		return errcodes.New(errcodes.Internal, "drop local copies of purged rows").WithCause(err)
	}
	if asJSON {
		printJSON(jsonKindTrashLocalPurged, dropped, len(dropped))
		return nil
	}
	fmt.Fprintln(os.Stderr, style.Muted(localTrashNote))
	verb := "dropped"
	if dry {
		verb = "would drop"
	}
	for _, r := range dropped {
		fmt.Printf("#%d %s/%s  %s\n", r.ID, r.Table, r.RowID, r.At)
	}
	fmt.Printf("%s %s %d local cop(ies) of purged rows\n", style.Success("✓"), verb, len(dropped))
	return nil
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
