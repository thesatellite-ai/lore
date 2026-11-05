// session_host.go — the old binary's opssession.Host (PASS3 ONE-PATH,
// P3-RUNS-4): `lore session list` is the operation session.list; this
// reads the machine-local session rows from the repo's lore.db.
//
//	session.list ─▶ sessionHost.ListSessions ─▶ sessions.List (newest first)
//	                                         └▶ audit_logs counts per session:
//	                                            WRITES = entries, NEW KEYS = distinct meta_keys rows (P3-RUNS-5)
package main

import (
	"context"
	"database/sql"
	"time"

	"dbent/gen/ent"
	"saas/pkg/aicoder/sessions"

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
	var f commonFlags
	rctx, client, err := resolveContext(&f)
	if err != nil {
		return nil, asCoreError(err)
	}
	defer joinClose(&err, client, "the project DB")
	rows, err := sessions.List(ctx, client, limit)
	if err != nil {
		return nil, err
	}
	// the counts read through a private read-only handle (P2B-OLD-3)
	db, err := openPrivateLoreDB(rctx.DBPath, true)
	if err != nil {
		return nil, err
	}
	defer joinClose(&err, db, "the project DB (read-only)")
	out := make([]opssession.Row, 0, len(rows))
	for _, s := range rows {
		r := sessionOpRow(s)
		if err := sessionCounts(ctx, db, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
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

// sessionOpRow maps one lore.db session row (raw ids; session.list redacts).
func sessionOpRow(s *ent.Session) opssession.Row {
	r := opssession.Row{ID: s.ID, Tool: s.Tool, ToolSessionID: s.ToolSessionID, ToolVersion: s.ToolVersion,
		Model: s.Model, MachineID: s.MachineID, RunID: s.RunID, Repo: s.Repo, Branch: s.Branch, Cwd: s.Cwd,
		Terminal: s.Terminal, CI: s.Ci, ParentSessionID: s.ParentSessionID, Signal: s.Signal,
		Sources: s.Sources, OuterTools: s.OuterTools, DeclaredHuman: s.DeclaredHuman, Flags: s.Flags,
		StartedAt: s.StartedAt.UTC().Format(time.RFC3339)}
	if s.ActorID != nil {
		r.ActorID = *s.ActorID
	}
	if s.EndedAt != nil {
		r.LastWriteAt = s.EndedAt.UTC().Format(time.RFC3339)
	}
	return r
}
