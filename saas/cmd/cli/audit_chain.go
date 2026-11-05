// audit_chain.go — the old binary's side of the audit chain in the machine
// store (P3-LOCAL-7): the v0.1.17 lore.db audit_logs chain, moved
// unchanged (same hash, same verify) into <lore home>/state/machine.db
// through wire.App.AuditChain, one chain per lore.db path.
//
//	appendAudit(app, dbPath, e)
//	  ├─ open the chain (fresh handle per call)
//	  ├─ importLoreDBAudit: first use of this chain on the machine → copy the
//	  │    lore.db audit_logs rows verbatim, oldest first (once: a marker row;
//	  │    run twice / two processes → the second copies nothing)
//	  └─ Append (BEGIN IMMEDIATE: head, link, insert)
//	auditRecords(app, dbPath) ─▶ the same import first, then the chain oldest first
//
// Nothing reads or writes lore.db audit_logs afterwards except this one
// import read, so phase E can delete ent. A v0.1.17 binary that keeps
// writing the old table after the import is not followed (its rows stay
// in lore.db).
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/auditchain"
	"github.com/thesatellite-ai/lore/wire"
)

// auditDBPath is the lore.db path of the project at root ("" = working
// directory), the chain's key source.
//
// P3-F18-1: a fresh clone of a 0.1.18 project has .lore/data and no lore.db
// (it is gitignored), so projresolve answers ErrNotProjectRoot there. The
// chain lives in the machine store since P3-LOCAL-7 and needs only a name,
// so such a folder names the lore.db path a later materialize builds
// (<root>/.lore/lore.db, projresolve's Mode A default): the chain key stays
// the same before and after lore.db exists. Before this, every core write
// in a fresh clone failed its audit record (the first actor row: E_INTERNAL
// "wire: write the actor row to store repo"; lore actor register:
// E_NOT_PROJECT_ROOT), because the sink no longer materialized lore.db.
func auditDBPath(root string) (string, error) {
	rctx, err := projresolve.Resolve(projresolve.Inputs{Dir: root})
	if err == nil {
		return rctx.DBPath, nil
	}
	if errors.Is(err, projresolve.ErrNotProjectRoot) {
		if p, ok := freshCloneDBPath(root); ok {
			return p, nil
		}
	}
	return "", mapResolveError(err)
}

// freshCloneDBPath is <root>/.lore/lore.db when root ("" = working
// directory) holds a .lore folder with a data folder but neither lore.db
// nor a pointer file (projresolve.DetectMode's ErrNotProjectRoot case).
func freshCloneDBPath(root string) (string, bool) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		root = wd
	}
	fi, err := os.Stat(filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName))
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return filepath.Join(root, projresolve.MarkerDir, projresolve.ModeAFile), true
}

// openAuditChain opens app's chain (nil = coreApp) and makes sure the
// chain of dbPath took its one import.
func openAuditChain(ctx context.Context, app *wire.App, dbPath string) (*sqlite.AuditChain, string, error) {
	if app == nil {
		app = coreApp
	}
	if app == nil {
		return nil, "", errcodes.New(errcodes.Internal, "audit: the 0.1.18 core is not mounted")
	}
	ch, err := app.AuditChain(ctx)
	if err != nil {
		return nil, "", errcodes.New(errcodes.Internal, "audit: open the chain").WithCause(err)
	}
	key := wire.AuditChainKey(dbPath)
	if err := importLoreDBAudit(ctx, ch, key, dbPath); err != nil {
		return nil, "", errors.Join(err, ch.Close())
	}
	return ch, key, nil
}

// importLoreDBAudit is wire.ImportLoreDBAudit on the CLI clock.
func importLoreDBAudit(ctx context.Context, ch *sqlite.AuditChain, key, dbPath string) error {
	return wire.ImportLoreDBAudit(ctx, ch, key, dbPath, cliClock().Now())
}

// appendAudit appends e to the chain of dbPath.
func appendAudit(ctx context.Context, app *wire.App, dbPath string, e auditchain.Record) (err error) {
	ch, key, err := openAuditChain(ctx, app, dbPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ch.Close()) }()
	if _, err = ch.Append(ctx, key, e, cliClock().Now()); err != nil {
		return errcodes.New(errcodes.Internal, "audit: append").WithCause(err)
	}
	return nil
}

// auditRecords is the chain of dbPath, oldest first.
func auditRecords(ctx context.Context, app *wire.App, dbPath string) (_ []auditchain.Record, err error) {
	ch, key, err := openAuditChain(ctx, app, dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, ch.Close()) }()
	return ch.Records(ctx, key)
}
