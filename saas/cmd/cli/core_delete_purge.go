// core_delete_purge.go: `lore resource delete` and `lore meta delete` take
// --purge / --confirm exactly like every other core delete (PASS3, FA-DEL-1).
//
// The other nouns get these two flags from addCompatFlags (core_mount.go),
// which also adds v0.1.17's --db / --project / --read-only and indented
// JSON to every command of the noun. resource and meta never had those in
// v0.1.17, so only their delete command gets the delete half here:
//
//	--purge, no --confirm ─► E_NEEDS_CONFIRM before anything runs (DQ2/DEL-R4)
//	--purge --confirm     ─► engine tombstones the rows; "purged for good" line
//	--confirm alone       ─► answers the question like --yes (soft delete) + purge hint
//	neither               ─► unchanged: soft delete, asks (Confirm Always)
package main

import (
	"bytes"
	"errors"
	"saas/pkg/constants"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/surface/cli"
)

// purgeOnlyDeletes are the delete operations outside coreNouns that get
// the shared --purge / --confirm pair (PASS3: like every core delete).
var purgeOnlyDeletes = []string{opssettings.OpResDelete, opssettings.OpMetaDelete}

// withDeletePurgeFlags adds --purge / --confirm to the mounted delete
// command of each purgeOnlyDeletes operation. A missing command (not
// mounted in this build) is skipped: nothing to wrap.
func withDeletePurgeFlags(root *cobra.Command, r *compatRunner) {
	for _, name := range purgeOnlyDeletes {
		noun, verb, ok := strings.Cut(name, ".")
		if !ok || verb != op.VerbDelete {
			continue
		}
		c, _, err := root.Find([]string{noun, verb})
		if err != nil || c.Name() != verb || !cli.Generated(c) || c.RunE == nil {
			continue
		}
		addDeletePurgeFlags(c, r)
	}
}

// addDeletePurgeFlags gives one delete command the --purge / --confirm
// semantics of addCompatFlags' delete branch (same flag names, same
// refusal, same output rewrite through purgedLines).
func addDeletePurgeFlags(c *cobra.Command, r *compatRunner) {
	var confirm, purge bool
	if c.Flags().Lookup(constants.FlagConfirm) == nil {
		c.Flags().BoolVar(&confirm, constants.FlagConfirm, false,
			"confirms a --purge (or a delete under policy trash.purge_on_delete); without them, v0.1.17's spelling of --yes")
	}
	if c.Flags().Lookup(flagPurge) == nil {
		c.Flags().BoolVar(&purge, flagPurge, false, "skip the trash: remove for good, only a tombstone stays (needs --confirm)")
	}
	inner := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r.purge = &engine.PurgeRequest{Asked: purge, Confirmed: confirm}
		switch {
		case purge && !confirm: // DQ2/DEL-R4: a purge never prompts, it needs the literal --confirm
			r.pending = errs.New(errs.NeedsConfirm, "--purge removes "+purgeTarget(args)+" for good, without the trash (only a tombstone stays)").
				WithHint("ask the person, then rerun with --confirm (--yes does not purge)")
		case cmd.Flags().Changed(constants.FlagConfirm):
			// FA-DEL-1: a plain delete is soft, so --confirm answers its
			// question like --yes.
			if fl := cmd.Flags().Lookup(cli.FlagYes); fl != nil {
				if err := fl.Value.Set("true"); err != nil {
					return err
				}
			}
		}
		if asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON); asJSON {
			return inner(cmd, args) // JSON carries the ids; no line to rewrite
		}
		// Buffered so the "moved to the trash" line can become "purged for
		// good", or get the DQ8 purge hint after a plain --confirm.
		var buf bytes.Buffer
		out := cmd.OutOrStdout()
		cmd.SetOut(&buf)
		err := inner(cmd, args)
		cmd.SetOut(out)
		b := buf.Bytes()
		if err == nil {
			b = purgedLines(b, r.purge, confirm)
		}
		_, werr := out.Write(b)
		return errors.Join(err, werr)
	}
}

// purgeTarget names what a refused --purge would have removed: the named
// row, or the bulk form's matches (resource delete --kind --where).
func purgeTarget(args []string) string {
	if len(args) == 0 {
		return "every matching row"
	}
	return strings.Join(args, " ")
}
