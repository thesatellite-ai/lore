// edit_commands.go — `<entity> edit <id>` subcommands for entities where
// in-place mutation makes sense (PM + metadata buckets). Knowledge entities
// with a supersede chain (memory/rule/decision/hotfix/pattern) get their
// own edit cmds in this file too; bodies CAN be edited via `edit`, but
// authoring a new row with `--supersedes=<old>` is preferred when you want
// audit trail
//
// Only flags the user actually passes are applied (cobra Changed() check)
package main

import (
	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// editTextField runs textnorm.Normalize unless empty-clear is intended
func editText(label, raw string) (string, error) {
	v, err := textnorm.Normalize(raw)
	if err != nil {
		return "", errcodes.New(errcodes.InvalidInput, label+": "+err.Error())
	}
	return v, nil
}

// openForEdit opens DB, resolves project, and returns (ctx-friendly) client
// Callers do their own UpdateOne / Get
func openForEdit(cmd *cobra.Command, f *commonFlags) (*ent.Client, error) {
	if err := refuseIfReadOnly(f); err != nil {
		return nil, err
	}
	_, client, err := resolveContext(f)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// ── plan ─────────────────────────────────────────────────────────────────

// ── tasklist ─────────────────────────────────────────────────────────────

// ── playbook ─────────────────────────────────────────────────────────────

// ── prompt ───────────────────────────────────────────────────────────────

// ── architecture-note ────────────────────────────────────────────────────

// ── behaviour ────────────────────────────────────────────────────────────

// ── cookbook-recipe ──────────────────────────────────────────────────────

// ── tastepref ────────────────────────────────────────────────────────────

// ── suggestion ───────────────────────────────────────────────────────────

// ── workflow ─────────────────────────────────────────────────────────────

// ── workspace ────────────────────────────────────────────────────────────

// ── pattern ──────────────────────────────────────────────────────────────

// ── rule ─────────────────────────────────────────────────────────────────

// ── decision ─────────────────────────────────────────────────────────────

// ── hotfix ───────────────────────────────────────────────────────────────
