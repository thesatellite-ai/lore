// audit_cmd.go — the old binary's reader of the v0.1.17 hash-chained audit
// log (in the machine store since P3-LOCAL-7, audit_chain.go; one chain per
// lore.db path) for `lore audit log|verify`, which are core
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
	"os"
	"slices"
	"strings"

	"dbent"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"

	"github.com/thesatellite-ai/lore/logic/auditchain"
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
	dbPath, err := auditDBPath(oldProjectDir())
	if err != nil {
		return nil, asCoreError(err)
	}
	recs, err := auditRecords(ctx, s.app, dbPath)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read audit log", err)
	}
	slices.Reverse(recs)
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	out := make([]opsaudit.Entry, 0, len(recs))
	for _, r := range recs {
		out = append(out, entryOf(r))
	}
	return out, nil
}

// AuditVerify implements opsaudit.Env. It records nothing.
func (s *coreAuditSink) AuditVerify(ctx context.Context) (opsaudit.Report, error) {
	dbPath, err := auditDBPath(oldProjectDir())
	if err != nil {
		return opsaudit.Report{}, asCoreError(err)
	}
	recs, err := auditRecords(ctx, s.app, dbPath)
	if err != nil {
		return opsaudit.Report{}, errs.Wrap(errs.Internal, "read audit log", err)
	}
	var rep opsaudit.Report
	rep.BrokenAt, rep.Entries = auditchain.Verify(recs)
	for _, r := range recs {
		if strings.HasSuffix(r.Action, "."+string(lsync.ChangeExternal)) {
			rep.ExternalWrites++
		}
	}
	// 2. rows changed in lore.db by something other than lore (the sync
	// bridge's lore.db, not the chain): only where lore.db exists.
	if fi, lerr := os.Lstat(dbPath); lerr != nil || !fi.Mode().IsRegular() {
		return rep, nil
	}
	db, rctx, err := openAuditDB()
	if err != nil {
		return rep, err
	}
	defer func() { _ = db.Close() }() // read-only: nothing to flush
	if syncApplies(rctx) {
		changed, uerr := lsync.UnexportedChanges(ctx, db)
		if uerr != nil {
			return rep, errs.Wrap(errs.Internal, "compare rows with the synced state", uerr)
		}
		for _, c := range changed {
			rep.ChangedOutside = append(rep.ChangedOutside, opsaudit.RowRef{Table: c.Table, ID: c.ID})
		}
	}
	return rep, nil
}

// entryOf is one chain record in the operation's shape (same JSON keys).
func entryOf(r auditchain.Record) opsaudit.Entry {
	return opsaudit.Entry{
		ActingProjectID: r.ActingProjectID, TargetProjectID: r.TargetProjectID, ActorID: r.ActorID, Action: r.Action,
		TargetTable: r.TargetTable, TargetID: r.TargetID, Override: r.Override, BeforeHash: r.BeforeHash, AfterHash: r.AfterHash,
		Reason: r.Reason, SessionID: r.SessionID, Confirmed: r.Confirmed, ID: r.ID, CreatedAt: r.CreatedAt, PrevLogHash: r.PrevLogHash,
	}
}
