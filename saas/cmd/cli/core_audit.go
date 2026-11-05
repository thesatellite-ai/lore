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
	"path/filepath"
	"strings"
	"sync"

	"dbent"
	"saas/pkg/aicoder/audit"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// coreAuditKinds names the chain action suffix of every core row action
// that is not a plain create/update (the v0.1.17 chain recorded every local
// change as "<table>.write"; a delete, trash, restore or purge is named so
// the log says what happened, OV9 / later.md cl-wire). The suffix is the
// engine action's own wire value, so the chain and the core agree.
var coreAuditKinds = map[engine.Action]string{
	engine.ActionDelete:  string(engine.ActionDelete),
	engine.ActionTrash:   string(engine.ActionTrash),
	engine.ActionRestore: string(engine.ActionRestore),
	engine.ActionPurge:   string(engine.ActionPurge),
}

// coreAuditSink is the engine.AuditSink of the old binary: one chain entry
// per core event, in its own immediate transaction (the DSN's
// _txlock=immediate serialises appenders, so two processes never fork the
// chain). lore.db is opened once per process, on the first event.
type coreAuditSink struct {
	// root is the repo top the core opened (wire.Options.RepoDir): lore.db
	// is resolved there, not at the working directory, so a write run
	// from a subfolder records its audit entry instead of failing after
	// the row was stored (OVERNIGHT Qs subfolder). "" = working directory.
	root string
	// app is the App whose writes this sink records, for the session
	// lookup; nil = the package's coreApp. init and the project seed set it
	// to their own minimal App: they run before the org's stores have a
	// local copy, so coreApp (full options) could not even read the
	// identity and every init write failed its audit (TestE10).
	app *wire.App
	// topOnce/top cache the git top of root (projectRoot).
	topOnce sync.Once
	top     string
	once    sync.Once
	db      *sql.DB
	err     error
	// session is this process's ses_ row (6.2), found or created once on
	// the first event that asks (Session).
	sesOnce sync.Once
	ses     string
	sesErr  error
}

var _ engine.AuditSink = (*coreAuditSink)(nil)

// Record implements engine.AuditSink. The entry carries names and ids only
// (engine.Event never holds a field value), so no secret reaches the log.
//
// Only the repo store's own writes are appended: lore.db sits inside the
// checkout and holds ONLY the repo's rows (CONFIG_GUIDE, F20/E11). A write
// to an attached store (a company store, the personal "me" store) names an
// id that must never sit in a client folder, so it is not chained here;
// its trail is the machine-local ~/.lore/state/audit/<sto id>.jsonl that
// the core's wrapping sink (wire routedAudit, wire.StoreAuditDir) appends
// before handing the event on to this sink.
func (s *coreAuditSink) Record(ctx context.Context, ev engine.Event) error {
	if ev.Store != store.SourceRepo {
		return nil
	}
	s.once.Do(func() { s.db, s.err = openCoreAuditDBAt(ctx, s.projectRoot(ctx)) })
	if s.err != nil {
		return s.err
	}
	kind := string(lsync.ChangeLocal)
	if k, ok := coreAuditKinds[ev.Action]; ok {
		kind = k
	}
	reason := ev.Op
	if !ev.Cascade.IsZero() {
		reason += " (cascade of " + ev.Cascade.String() + ")"
	}
	table := ev.Table.String()
	action := table + "." + kind
	if ev.Secret != (engine.SecretAudit{}) {
		// a secret.* event (SECRETS 16): no row write; FORCED is loud.
		action = string(ev.Action)
		reason += " " + ev.Secret.Command + " " + ev.Secret.Result
		if ev.Secret.Forced {
			reason += " FORCED (" + string(ev.Secret.ForcedBy) + ")"
		}
	}
	ses := ev.Session
	if ses == "" {
		var serr error
		if ses, serr = s.Session(ctx); serr != nil {
			return serr
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errcodes.New(errcodes.Internal, "audit: begin").WithCause(err)
	}
	if err = audit.Append(ctx, tx, audit.Entry{
		ActorID: ev.Actor.String(), Action: action,
		TargetTable: table, TargetID: ev.ID.String(), Reason: reason,
		SessionID: ses, Confirmed: ev.Confirmed || engine.Confirmed(ctx), // 6.6, hashed only when set (OQ10)
	}); err != nil {
		return errcodes.New(errcodes.Internal, "audit: append a core write").WithCause(errors.Join(err, tx.Rollback()))
	}
	if err = tx.Commit(); err != nil {
		return errcodes.New(errcodes.Internal, "audit: commit").WithCause(err)
	}
	return nil
}

// projectRoot is the folder lore.db is resolved at: the nearest folder
// at or above root that holds a .lore marker, never climbing past the
// git top (root is any folder inside the repo, like wire.Options.RepoDir).
// OVERNIGHT Qs subfolder: a write run from repo/sub must chain into
// repo/.lore/lore.db; a project that lives in a sub-folder of the repo
// (repo/svc/.lore, several projects sharing one git repo) must chain into
// its own lore.db, not the git top's. With no marker on the way up it is
// the git top, else root itself ("" = working directory).
func (s *coreAuditSink) projectRoot(ctx context.Context) string {
	s.topOnce.Do(func() {
		s.top = s.root
		if s.root == "" {
			return
		}
		top, err := gitOutput(ctx, s.root, "rev-parse", "--show-toplevel")
		if err != nil || top == "" {
			return
		}
		s.top = nearestMarkerDir(s.root, top)
	})
	return s.top
}

// nearestMarkerDir walks from dir up to stop (inclusive) and returns the
// first folder that holds a projresolve.MarkerDir; stop when none does.
// dir must lie inside stop; a dir outside it returns stop at once.
func nearestMarkerDir(dir, stop string) string {
	stop = filepath.Clean(stop)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if real, err := filepath.EvalSymlinks(stop); err == nil {
		stop = real
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, projresolve.MarkerDir)); err == nil && fi.IsDir() {
			return d
		}
		rel, err := filepath.Rel(stop, d)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Dir(d) == d {
			return stop
		}
	}
}

// Session implements wire.SessionSource: this process's ses_ id in the
// repo's lore.db (machine-local, OQ14), "" when no session applies or
// there is no lore.db here. Found or created once per process.
func (s *coreAuditSink) Session(ctx context.Context) (string, error) {
	s.sesOnce.Do(func() {
		s.once.Do(func() { s.db, s.err = openCoreAuditDBAt(ctx, s.projectRoot(ctx)) })
		if s.err != nil {
			return // no repo lore.db (attached-store-only run): no session link
		}
		s.ses, s.sesErr = currentSession(ctx, s.app, s.db, s.projectRoot(ctx))
	})
	return s.ses, s.sesErr
}

// openCoreAuditDB opens the project's lore.db for the chain, building it
// from .lore/data first in a fresh clone (as every old command does).
func openCoreAuditDB(ctx context.Context) (*sql.DB, error) {
	return openCoreAuditDBAt(ctx, "")
}

// openCoreAuditDBAt is openCoreAuditDB for the project at root ("" = the
// working directory; OVERNIGHT Qs subfolder).
func openCoreAuditDBAt(ctx context.Context, root string) (*sql.DB, error) {
	if os.Getenv(envDBPath) == "" {
		dir := root
		if dir == "" {
			wd, err := os.Getwd()
			if err != nil {
				return nil, errcodes.New(errcodes.Internal, "audit: read the working directory").WithCause(err)
			}
			dir = wd
		}
		if err := materializeAt(ctx, dir); err != nil {
			return nil, materializeError(err)
		}
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{Dir: root})
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

// coreEnsureActor writes the current identity's actor row file into the
// repo store through the core. Outside a git work tree (a --db command)
// there is no repo store and nothing to write.
func coreEnsureActor(ctx context.Context) error {
	if coreApp == nil {
		return nil
	}
	err := coreApp.EnsureActor(ctx, store.SourceRepo)
	if errs.Has(err, errs.NotAStore) {
		return nil
	}
	return err
}
