// who_core.go — the session lines of `lore task show --who` (AGENT_IDENTITY
// 6.2 / 9): the old binary's audit sink owns lore.db, so it implements
// wire.WhoSource and the core's (*wire.App).WhoLines hands the question on.
//
//	opstask.who → (*wire.App).WhoLines → (*coreAuditSink).WhoLines
//	  ├─ open lore.db (the sink's handle; none here → no lines)
//	  ├─ audit_logs rows of the target with a session_id, grouped per session
//	  └─ each ses_ id looked up in the machine store's sessions (tool, tool
//	     session id, model; P3-COORD-SESS-1); the tool session id is
//	     redacted off-terminal / in CI
//
// Both are machine-local (Q-LOCAL): nothing here reaches .lore/data.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/wire"
)

var _ wire.WhoSource = (*coreAuditSink)(nil)

// whoNone is printed for a session field lore never learned (OQ8: never
// guessed).
const whoNone = "-"

// whoSelect lists the sessions that wrote one row, oldest first.
const whoSelect = `SELECT a.session_id, MAX(COALESCE(a.confirmed, 0)), COUNT(*)
	FROM audit_logs a
	WHERE a.target_id = ? AND COALESCE(a.session_id, '') <> ''
	GROUP BY a.session_id ORDER BY MIN(a.created_at)`

// WhoLines implements wire.WhoSource: one line per session that wrote
// rowID — ses_ id, tool, tool session (redacted), model, confirmed, writes.
func (s *coreAuditSink) WhoLines(ctx context.Context, rowID string) ([]string, error) {
	s.once.Do(func() { s.db, s.err = openCoreAuditDB(ctx) })
	if s.err != nil {
		return nil, nil // no repo lore.db here: nothing machine-local to show
	}
	if ok, err := columnExists(ctx, s.db, "audit_logs", "session_id"); err != nil || !ok {
		return nil, err // a v0.1.17 lore.db not migrated yet: no session links
	}
	known, err := s.whoSessions(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, whoSelect, rowID)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the audit log").WithCause(err)
	}
	defer func() { _ = rows.Close() }() // read-only
	var out []string
	for rows.Next() {
		var ses string
		var confirmed, writes int
		if err := rows.Scan(&ses, &confirmed, &writes); err != nil {
			return nil, errcodes.New(errcodes.Internal, "who: scan the audit log").WithCause(err)
		}
		d := known[ses]
		out = append(out, fmt.Sprintf("%s  tool %s  session %s  model %s  confirmed %s  (%d writes)",
			ses, whoField(d.Tool), whoField(redactToolSession(d.ToolSessionID)), whoField(d.Model), yesNoWho(confirmed != 0), writes))
	}
	if err := rows.Err(); err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the audit log").WithCause(err)
	}
	return out, nil
}

// whoSessions maps each machine-store ses_ id to its details (empty when
// the core is not mounted: the lines then show "-").
func (s *coreAuditSink) whoSessions(ctx context.Context) (map[string]wire.SessionDetails, error) {
	app := s.app
	if app == nil {
		app = coreApp
	}
	out := map[string]wire.SessionDetails{}
	if app == nil {
		return out, nil
	}
	rows, err := app.Sessions(ctx, 0)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the sessions").WithCause(err)
	}
	for _, r := range rows {
		out[r.ID] = r.SessionDetails
	}
	return out, nil
}

// columnExists reports whether table has the named column.
func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	if err != nil {
		return false, errcodes.New(errcodes.Internal, "who: read the "+table+" columns").WithCause(err)
	}
	return n == 1, nil
}

// whoField shows an unknown field as "-".
func whoField(v string) string {
	if strings.TrimSpace(v) == "" {
		return whoNone
	}
	return v
}

// yesNoWho spells the confirmed flag.
func yesNoWho(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
