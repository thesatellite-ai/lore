// audit_cmd.go — the old binary's reader of the v0.1.17 hash-chained audit
// log (lore.db audit_logs) for `lore audit log|verify`, which are core
// operations now (ops/audit, P3-AUDIT-1..4): coreAuditSink, which appends
// the chain, also implements opsaudit.Env, and wire hands the operations
// to it (wire/auditlog.go).
//
// verify checks two things:
//
//  1. the chain: editing or deleting a past entry breaks the link of the
//     entry after it (E_AUDIT_CHAIN_BROKEN);
//  2. rows changed in lore.db by something other than lore since the last
//     lore command (sqlite3, a script): "hash mismatch".
//
// The DB is opened WITHOUT a sync pass: a pass would export, and audit as
// accepted, the very changes verify looks for.
package main

import (
	"context"
	"database/sql"
	"strings"

	"dbent"
	"saas/pkg/aicoder/audit"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"

	"github.com/thesatellite-ai/lore/model/errs"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
)

var _ opsaudit.Env = (*coreAuditSink)(nil)

// openAuditDB opens the project DB with no sync pass and no session
// registration (see file doc). The project is the one the working
// directory resolves to, as v0.1.17's `lore audit` did.
func openAuditDB() (*sql.DB, *projresolve.Context, error) {
	rctx, err := projresolve.Resolve(projresolve.Inputs{Dir: oldProjectDir()})
	if err != nil {
		return nil, nil, asCoreError(mapResolveError(err))
	}
	db := dbent.InitDB(rctx.DBPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		_ = db.Close() // the pragma error is the one to report
		return nil, nil, errs.Wrap(errs.Internal, "apply pragmas", err)
	}
	return db, rctx, nil
}

// AuditLog implements opsaudit.Env: newest first, limit <= 0 = all.
func (s *coreAuditSink) AuditLog(ctx context.Context, limit int) ([]opsaudit.Entry, error) {
	db, _, err := openAuditDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }() // read-only: nothing to flush
	recs, err := audit.List(ctx, db, limit)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read audit log", err)
	}
	out := make([]opsaudit.Entry, 0, len(recs))
	for _, r := range recs {
		out = append(out, entryOf(r))
	}
	return out, nil
}

// AuditVerify implements opsaudit.Env. It records nothing.
func (s *coreAuditSink) AuditVerify(ctx context.Context) (opsaudit.Report, error) {
	db, rctx, err := openAuditDB()
	if err != nil {
		return opsaudit.Report{}, err
	}
	defer func() { _ = db.Close() }() // read-only: nothing to flush
	return runAuditVerify(ctx, db, rctx)
}

// runAuditVerify walks the chain, compares the rows with the synced state
// and counts the external writes recorded earlier.
func runAuditVerify(ctx context.Context, db *sql.DB, rctx *projresolve.Context) (opsaudit.Report, error) {
	var rep opsaudit.Report
	broken, n, err := audit.Verify(ctx, db)
	if err != nil {
		return rep, errs.Wrap(errs.Internal, "verify audit chain", err)
	}
	rep.Entries, rep.BrokenAt = n, broken
	if syncApplies(rctx) {
		changed, uerr := lsync.UnexportedChanges(ctx, db)
		if uerr != nil {
			return rep, errs.Wrap(errs.Internal, "compare rows with the synced state", uerr)
		}
		for _, c := range changed {
			rep.ChangedOutside = append(rep.ChangedOutside, opsaudit.RowRef{Table: c.Table, ID: c.ID})
		}
	}
	recs, err := audit.List(ctx, db, 0)
	if err != nil {
		return rep, errs.Wrap(errs.Internal, "read audit log", err)
	}
	for _, r := range recs {
		if strings.HasSuffix(r.Action, "."+string(lsync.ChangeExternal)) {
			rep.ExternalWrites++
		}
	}
	return rep, nil
}

// entryOf is one chain record in the operation's shape (same JSON keys).
func entryOf(r audit.Record) opsaudit.Entry {
	return opsaudit.Entry{
		ActingProjectID: r.ActingProjectID, TargetProjectID: r.TargetProjectID, ActorID: r.ActorID, Action: r.Action,
		TargetTable: r.TargetTable, TargetID: r.TargetID, Override: r.Override, BeforeHash: r.BeforeHash, AfterHash: r.AfterHash,
		Reason: r.Reason, SessionID: r.SessionID, Confirmed: r.Confirmed, ID: r.ID, CreatedAt: r.CreatedAt, PrevLogHash: r.PrevLogHash,
	}
}
