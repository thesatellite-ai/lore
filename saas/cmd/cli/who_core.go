// who_core.go — the session lines of `lore task show --who` (AGENT_IDENTITY
// 6.2 / 9): the old binary's audit sink owns lore.db, so it implements
// wire.WhoSource and the core's (*wire.App).WhoLines hands the question on.
//
//	opstask.who → (*wire.App).WhoLines → (*coreAuditSink).WhoLines
//	  ├─ open lore.db (the sink's handle; none here → no lines)
//	  ├─ audit_logs rows of the target with a session_id, grouped per session
//	  └─ LEFT JOIN sessions (tool, tool session id, model) when the 0.1.18
//	     columns exist; the tool session id is redacted off-terminal / in CI
//
// Both tables are machine-local (Q-LOCAL): nothing here reaches .lore/data.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/sessions"

	"github.com/thesatellite-ai/lore/wire"
)

var _ wire.WhoSource = (*coreAuditSink)(nil)

// whoNone is printed for a session field lore never learned (OQ8: never
// guessed).
const whoNone = "-"

// whoSelectJoined and whoSelectBare list the sessions that wrote one row, oldest first. The
// sessions join is added only when lore.db has the 0.1.18 columns.
const (
	whoSelectJoined = `SELECT a.session_id, MAX(COALESCE(a.confirmed, 0)), COUNT(*),
	COALESCE(s.tool, ''), COALESCE(s.tool_session_id, ''), COALESCE(s.model, '')
	FROM audit_logs a LEFT JOIN sessions s ON s.id = a.session_id
	WHERE a.target_id = ? AND COALESCE(a.session_id, '') <> ''
	GROUP BY a.session_id ORDER BY MIN(a.created_at)`
	whoSelectBare = `SELECT a.session_id, MAX(COALESCE(a.confirmed, 0)), COUNT(*), '', '', ''
	FROM audit_logs a
	WHERE a.target_id = ? AND COALESCE(a.session_id, '') <> ''
	GROUP BY a.session_id ORDER BY MIN(a.created_at)`
)

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
	q := whoSelectBare
	if ready, err := sessions.Ready(ctx, s.db); err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the sessions columns").WithCause(err)
	} else if ready {
		q = whoSelectJoined
	}
	rows, err := s.db.QueryContext(ctx, q, rowID)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the audit log").WithCause(err)
	}
	defer func() { _ = rows.Close() }() // read-only
	var out []string
	for rows.Next() {
		var ses, tool, toolSes, model string
		var confirmed, writes int
		if err := rows.Scan(&ses, &confirmed, &writes, &tool, &toolSes, &model); err != nil {
			return nil, errcodes.New(errcodes.Internal, "who: scan the audit log").WithCause(err)
		}
		out = append(out, fmt.Sprintf("%s  tool %s  session %s  model %s  confirmed %s  (%d writes)",
			ses, whoField(tool), whoField(redactToolSession(toolSes)), whoField(model), yesNoWho(confirmed != 0), writes))
	}
	if err := rows.Err(); err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the audit log").WithCause(err)
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
