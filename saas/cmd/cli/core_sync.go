// core_sync.go — the lore.db read copy kept current around a core command.
//
// v0.1.17 ran a sync pass at the start and end of EVERY command, so lore.db
// mirrored .lore/data after each one. The core writes row files only, which
// left two holes once a project has a lore.db:
//
//   - a row a core command wrote was not in lore.db until some old command
//     ran, so `lore audit verify` (which deliberately runs no pass) compared
//     nothing and a sqlite3 edit of that row verified as intact;
//   - a sqlite3 edit of lore.db was never recorded as an external write by
//     a core command (`lore rule list`), as every v0.1.17 command did.
//
// So a core CLI command in a project with lore.db runs the catch-up pass
// before it (preCommandSync: teammates' files imported, lore.db-only
// changes audited as *.external-write) and a full pass after it
// (finishSyncSessions, coreFullFinish) that imports the files the command
// wrote. REST (`lore api`) never runs these: one server process serves
// many requests and keeps no per-command session.
package main

import (
	"context"
	"os"

	"dbent"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
)

// coreSyncBefore runs the start-of-command pass for a core command when the
// project resolves to a lore.db (Mode A, sync on) and registers the sync
// session so main's finishSyncSessions runs the end pass. Read-only mode
// writes nothing, lore.db included. Never fails the command: a pass error
// is printed by preCommandSync and retried on the next command.
func coreSyncBefore(ctx context.Context, readOnly bool) {
	if readOnly || os.Getenv(envReadOnly) == envValueOn {
		return
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{})
	if err != nil || !syncApplies(rctx) {
		return
	}
	if fi, serr := os.Lstat(rctx.DBPath); serr != nil || !fi.Mode().IsRegular() {
		return // no read copy (fresh clone) or a symlink: the core needs neither
	}
	db := dbent.InitDB(rctx.DBPath)
	defer func() { _ = db.Close() }() // the pass committed; nothing to flush
	if dbent.ApplyPragmas(db) != nil {
		return
	}
	preCommandSync(ctx, rctx, db)
	if s := syncSessions[rctx.DBPath]; s != nil {
		s.coreFullFinish = true
	}
}

// coreFinishOptions are the end-of-command pass options of a session a core
// command ran in: a full pass (not dirty-only), so the row files the
// command wrote are imported into lore.db. Their import is not audited
// again: the core's audit sink (core_audit.go) already recorded each write
// as <table>.write / .delete; every other change keeps its audit entry.
func coreFinishOptions(o lsync.Options) lsync.Options {
	inner := o.OnChange
	if inner == nil {
		return o
	}
	o.OnChange = func(ctx context.Context, q lsync.Execer, c lsync.Change) error {
		if c.Kind == lsync.ChangeImport && lsync.CoreOwned(c.Table) {
			return nil
		}
		return inner(ctx, q, c)
	}
	return o
}

// coreProjectHint is wire.Options.ProjectHint: the project v0.1.17 would
// write to here, the one live project of the checkout's lore.db (an
// explicit LORE_PROJECT_ID / lore.yaml id first, as projresolve orders
// them). Why: a clone whose own `lore init` project sits beside the one
// teammates committed (not adopted into one yet: LORE_SYNC=0, or before
// the first pass) keeps writing to its own project, as v0.1.17 did,
// instead of failing E_AMBIGUOUS_PROJECT. Called only in that ambiguous
// case; no lore.db, or not exactly one project in it, is no hint.
func coreProjectHint(ctx context.Context) (string, bool) {
	rctx, err := projresolve.Resolve(projresolve.Inputs{})
	if err != nil {
		return "", false
	}
	if rctx.ProjectID != "" {
		return rctx.ProjectID, true
	}
	if fi, serr := os.Lstat(rctx.DBPath); serr != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	db := dbent.InitDB(rctx.DBPath)
	defer func() { _ = db.Close() }() // read only
	rows, err := db.QueryContext(ctx, `SELECT id FROM projects WHERE archived_at IS NULL LIMIT 2`)
	if err != nil {
		return "", false
	}
	defer func() { _ = rows.Close() }() // read only
	var found []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return "", false
		}
		found = append(found, id)
	}
	if rows.Err() != nil || len(found) != 1 {
		return "", false
	}
	return found[0], true
}
