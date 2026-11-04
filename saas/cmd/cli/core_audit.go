// core_audit.go — the 0.1.18 core's audit events appended to the v0.1.17
// hash-chained audit log (lore.db audit_logs), so `lore audit log` and
// `lore audit verify` see core writes as they saw every v0.1.17 write.
//
// The chain writer (saas/pkg/aicoder/audit) lives in the old module, so the
// sink is built here and handed to the core through wire.Options.Audit; the
// new module never imports saas.
package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"

	"dbent"
	"saas/pkg/aicoder/audit"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"

	"github.com/thesatellite-ai/lore/engine"
)

// coreAuditDeleteKind is the action suffix of a core tombstone write (the
// v0.1.17 chain recorded every local change as "<table>.write"; a core
// delete is named so the log says what happened).
const coreAuditDeleteKind = "delete"

// coreAuditSink is the engine.AuditSink of the old binary: one chain entry
// per core event, in its own immediate transaction (the DSN's
// _txlock=immediate serialises appenders, so two processes never fork the
// chain). lore.db is opened once per process, on the first event.
type coreAuditSink struct {
	once sync.Once
	db   *sql.DB
	err  error
}

var _ engine.AuditSink = (*coreAuditSink)(nil)

// Record implements engine.AuditSink. The entry carries names and ids only
// (engine.Event never holds a field value), so no secret reaches the log.
func (s *coreAuditSink) Record(ctx context.Context, ev engine.Event) error {
	s.once.Do(func() { s.db, s.err = openCoreAuditDB(ctx) })
	if s.err != nil {
		return s.err
	}
	kind := string(lsync.ChangeLocal)
	if ev.Action == engine.ActionDelete {
		kind = coreAuditDeleteKind
	}
	reason := ev.Op
	if !ev.Cascade.IsZero() {
		reason += " (cascade of " + ev.Cascade.String() + ")"
	}
	table := ev.Table.String()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errcodes.New(errcodes.Internal, "audit: begin").WithCause(err)
	}
	if err = audit.Append(ctx, tx, audit.Entry{
		ActorID: ev.Actor.String(), Action: table + "." + kind,
		TargetTable: table, TargetID: ev.ID.String(), Reason: reason,
	}); err != nil {
		return errcodes.New(errcodes.Internal, "audit: append a core write").WithCause(errors.Join(err, tx.Rollback()))
	}
	if err = tx.Commit(); err != nil {
		return errcodes.New(errcodes.Internal, "audit: commit").WithCause(err)
	}
	return nil
}

// openCoreAuditDB opens the project's lore.db for the chain, building it
// from .lore/data first in a fresh clone (as every old command does).
func openCoreAuditDB(ctx context.Context) (*sql.DB, error) {
	var none commonFlags
	if err := maybeMaterialize(ctx, &none); err != nil {
		return nil, materializeError(err)
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{})
	if err != nil {
		return nil, mapResolveError(err)
	}
	if li, lerr := os.Lstat(rctx.DBPath); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
		return nil, errcodes.New(errcodes.SymlinkDB, "refusing to open lore.db: it is a symlink")
	}
	db := dbent.InitDB(rctx.DBPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		return nil, errcodes.New(errcodes.Internal, "apply pragmas").WithCause(errors.Join(err, db.Close()))
	}
	return db, nil
}
