// who_core.go — the session lines of `lore task show --who` (AGENT_IDENTITY
// 6.2 / 9): the old binary's audit sink keeps the repo's audit chain, so it
// implements wire.WhoSource and the core's (*wire.App).WhoLines hands the
// question on.
//
//	opstask.who → (*wire.App).WhoLines → (*coreAuditSink).WhoLines
//	  ├─ resolve the project's chain (no project here → no lines)
//	  ├─ audit chain entries of the target with a session_id (machine store, P3-LOCAL-7), grouped per session
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

// WhoLines implements wire.WhoSource: one line per session that wrote
// rowID — ses_ id, tool, tool session (redacted), model, confirmed, writes
// — oldest first (the session's first write), from the audit chain in the
// machine store (P3-LOCAL-7).
func (s *coreAuditSink) WhoLines(ctx context.Context, rowID string) ([]string, error) {
	s.once.Do(func() { s.dbPath, s.err = auditDBPath(s.projectRoot(ctx)) })
	if s.err != nil {
		return nil, nil // no project here: nothing machine-local to show
	}
	recs, err := auditRecords(ctx, s.app, s.dbPath)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "who: read the audit log").WithCause(err)
	}
	type tally struct {
		confirmed bool
		writes    int
	}
	var order []string
	per := map[string]*tally{}
	for _, r := range recs {
		if r.TargetID != rowID || strings.TrimSpace(r.SessionID) == "" {
			continue
		}
		t, ok := per[r.SessionID]
		if !ok {
			t = &tally{}
			per[r.SessionID] = t
			order = append(order, r.SessionID)
		}
		t.writes++
		t.confirmed = t.confirmed || r.Confirmed
	}
	if len(order) == 0 {
		return nil, nil
	}
	known, err := s.whoSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(order))
	for _, ses := range order {
		d, t := known[ses], per[ses]
		out = append(out, fmt.Sprintf("%s  tool %s  session %s  model %s  confirmed %s  (%d writes)",
			ses, whoField(d.Tool), whoField(redactToolSession(d.ToolSessionID)), whoField(d.Model), yesNoWho(t.confirmed), t.writes))
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
