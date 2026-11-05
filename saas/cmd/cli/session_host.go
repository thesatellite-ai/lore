// session_host.go — the old binary's opssession.Host (PASS3 ONE-PATH,
// P3-RUNS-4): `lore session list` is the operation session.list; the
// session rows come from the machine store (P3-COORD-SESS-1), the counts
// from the repo's lore.db audit chain.
//
//	session.list ─▶ sessionHost.ListSessions ─▶ coreApp.SessionOpRows (newest first)
//	                                         └▶ audit_logs counts per session:
//	                                            WRITES = entries, NEW KEYS = distinct meta_keys rows (P3-RUNS-5)
package main

import (
	"context"
	"database/sql"
	"os"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/projresolve"

	opssession "github.com/thesatellite-ai/lore/ops/session"
	"github.com/thesatellite-ai/lore/schema"
)

// sessionHost is wire.SessionHosts.Session.
type sessionHost struct{}

var _ opssession.Host = sessionHost{}

// RevealToolSessions implements opssession.Host: raw ids only on a
// terminal outside CI (the whoami rule, quietOutput).
func (sessionHost) RevealToolSessions(context.Context) bool { return !quietOutput() }

// ListSessions implements opssession.Host.
func (sessionHost) ListSessions(ctx context.Context, limit int) (_ []opssession.Row, err error) {
	if coreApp == nil {
		return nil, errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	out, err := coreApp.SessionOpRows(ctx, limit)
	if err != nil || len(out) == 0 {
		return out, err
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{Dir: oldProjectDir()})
	if err != nil {
		return out, nil // no project here: the rows without counts
	}
	if fi, lerr := os.Lstat(rctx.DBPath); lerr != nil || !fi.Mode().IsRegular() {
		return out, nil // no lore.db audit chain (or a symlink lore never reads): counts stay zero
	}
	// the counts read through a private read-only handle (P2B-OLD-3)
	db, err := openPrivateLoreDB(rctx.DBPath, true)
	if err != nil {
		return nil, err
	}
	defer joinClose(&err, db, "the project DB (read-only)")
	if ok, cerr := columnExists(ctx, db, "audit_logs", "session_id"); cerr != nil || !ok {
		return out, cerr // a v0.1.17 lore.db not migrated yet: no session links
	}
	for i := range out {
		if err := sessionCounts(ctx, db, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// sessionCounts fills r.Writes and r.NewKeys from audit_logs.
func sessionCounts(ctx context.Context, db *sql.DB, r *opssession.Row) error {
	q := `SELECT COUNT(*), COUNT(DISTINCT CASE WHEN target_table = ? THEN target_id END) FROM audit_logs WHERE session_id = ?`
	rows, err := db.QueryContext(ctx, q, schema.TableMetaKeys, r.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() // read-only; a close error after the scan is not actionable
	if rows.Next() {
		var writes, keys sql.NullInt64
		if err := rows.Scan(&writes, &keys); err != nil {
			return err
		}
		r.Writes, r.NewKeys = int(writes.Int64), int(keys.Int64)
	}
	return rows.Err()
}
