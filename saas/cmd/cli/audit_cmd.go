// audit_cmd.go — `lore audit verify` / `lore audit log`.
//
// The audit log is appended by every sync pass (sync_wire.go auditChange):
// one hash-chained entry per changed shared row. verify checks two things:
//
//  1. the chain: editing or deleting a past entry breaks the link of the
//     entry after it (E_AUDIT_CHAIN_BROKEN);
//  2. rows changed in lore.db by something other than lore since the last
//     lore command (sqlite3, a script): "hash mismatch".
//
// These commands deliberately open the DB WITHOUT a sync pass: a pass would
// export — and audit as accepted — the very changes verify looks for.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"dbent"
	"saas/pkg/aicoder/audit"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// JSON envelope kinds for the audit commands.
const (
	jsonKindAuditVerify = "audit.verify"
	jsonKindAuditLog    = "audit.log"
)

// externalWriteSuffix ends the action of entries recorded for changes lore
// found in the DB but did not make.
const externalWriteSuffix = "." + string(lsync.ChangeExternal)

// defaultAuditLogLimit is how many entries `lore audit log` shows.
const defaultAuditLogLimit = 50

// auditVerifyResult is the --json shape of `lore audit verify`.
type auditVerifyResult struct {
	OK             bool                     `json:"ok"`
	Entries        int                      `json:"entries"`
	ChainBrokenAt  string                   `json:"chain_broken_at,omitempty"`
	ChangedOutside []lsync.UnexportedChange `json:"changed_outside_lore,omitempty"`
	ExternalWrites int                      `json:"external_writes_recorded"`
}

// openAuditDB opens the project DB with no sync pass and no session
// registration (see file doc).
func openAuditDB(c *commonFlags) (*sql.DB, *projresolve.Context, error) {
	rctx, err := projresolve.Resolve(projresolve.Inputs{FlagDB: c.flagDB, FlagProject: c.flagProject})
	if err != nil {
		return nil, nil, mapResolveError(err)
	}
	db := dbent.InitDB(rctx.DBPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		_ = db.Close() // the pragma error is the one to report
		return nil, nil, errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	return db, rctx, nil
}

func newAuditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect and verify the hash-chained audit log of knowledge changes",
		Long: `Every change to a shared row (rules, memories, tasks, …) is recorded in a
hash-chained audit log: lore's own writes, imports from teammates via
.lore/data, and changes lore found in the DB that no lore command made.`,
	}
	cmd.AddCommand(newAuditVerifyCommand(), newAuditLogCommand())
	return cmd
}

func newAuditVerifyCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check the audit chain and look for rows changed outside lore",
		Long: `verify exits non-zero when:
  • the audit chain is broken (an entry was edited or deleted), or
  • a shared row in lore.db differs from what lore last recorded and no lore
    command made the change (hash mismatch: e.g. an edit with sqlite3).

Run it BEFORE other lore commands when investigating: any lore command
synchronises the DB and records such changes as external writes (listed by
` + "`lore audit log`" + `).`,
		Example: "  lore audit verify\n  lore audit verify --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, rctx, err := openAuditDB(&f)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }() // read-only: nothing to flush
			res, err := runAuditVerify(cmd.Context(), db, rctx)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(jsonKindAuditVerify, res, res.Entries)
			} else {
				printAuditVerify(res)
			}
			switch {
			case res.ChainBrokenAt != "":
				return errcodes.New(errcodes.AuditChainBroken, "audit chain broken at "+res.ChainBrokenAt).
					WithHint("an audit entry was edited or deleted; restore lore.db from a backup to investigate")
			case len(res.ChangedOutside) > 0:
				return errcodes.New(errcodes.AuditChainBroken,
					fmt.Sprintf("hash mismatch: %d row(s) changed outside lore", len(res.ChangedOutside))).
					WithHint("inspect them (`lore <kind> show <id>`); the next lore command records them as external writes")
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

func runAuditVerify(ctx context.Context, db *sql.DB, rctx *projresolve.Context) (auditVerifyResult, error) {
	var res auditVerifyResult
	broken, n, err := audit.Verify(ctx, db)
	if err != nil {
		return res, errcodes.New(errcodes.Internal, "verify audit chain").WithCause(err)
	}
	res.Entries, res.ChainBrokenAt = n, broken
	if syncApplies(rctx) {
		changed, err := lsync.UnexportedChanges(ctx, db)
		if err != nil {
			return res, errcodes.New(errcodes.Internal, "compare rows with the synced state").WithCause(err)
		}
		res.ChangedOutside = changed
	}
	recs, err := audit.List(ctx, db, 0)
	if err != nil {
		return res, errcodes.New(errcodes.Internal, "read audit log").WithCause(err)
	}
	for _, r := range recs {
		if strings.HasSuffix(r.Action, externalWriteSuffix) {
			res.ExternalWrites++
		}
	}
	res.OK = res.ChainBrokenAt == "" && len(res.ChangedOutside) == 0
	return res, nil
}

func printAuditVerify(res auditVerifyResult) {
	if res.ChainBrokenAt != "" {
		fmt.Println(style.Error("✗ audit chain broken at " + res.ChainBrokenAt + " (an entry was edited or deleted)"))
	} else {
		fmt.Printf("%s audit chain intact (%d entries)\n", style.Success("✓"), res.Entries)
	}
	for _, c := range res.ChangedOutside {
		fmt.Println(style.Error(fmt.Sprintf("✗ hash mismatch: %s/%s changed outside lore", c.Table, c.ID)))
	}
	if res.ExternalWrites > 0 {
		fmt.Println(style.Warn(fmt.Sprintf("! %d change(s) were made outside lore and later recorded (action *.external-write; see `lore audit log`)", res.ExternalWrites)))
	}
}

func newAuditLogCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	var limit int
	cmd := &cobra.Command{
		Use:     "log",
		Short:   "Show recent audit entries, newest first",
		Example: "  lore audit log\n  lore audit log --limit 200 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, _, err := openAuditDB(&f)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }() // read-only: nothing to flush
			recs, err := audit.List(cmd.Context(), db, limit)
			if err != nil {
				return errcodes.New(errcodes.Internal, "read audit log").WithCause(err)
			}
			if asJSON {
				printJSON(jsonKindAuditLog, recs, len(recs))
				return nil
			}
			if len(recs) == 0 {
				fmt.Println(style.Muted("· audit log is empty"))
			}
			for _, r := range recs {
				fmt.Printf("%s  %-28s %s/%s  by %s\n", r.CreatedAt, r.Action, r.TargetTable, r.TargetID, r.ActorID)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	cmd.Flags().IntVar(&limit, constants.FlagLimit, defaultAuditLogLimit, "entries to show (0 = all)")
	return cmd
}
