// session_host.go — the old binary's opssession.Host (PASS3 ONE-PATH,
// P3-RUNS-4): `lore session list` is the operation session.list; the
// session rows come from the machine store (P3-COORD-SESS-1), the counts
// from the repo's lore.db audit chain.
//
//	session.list ─▶ sessionHost.ListSessions ─▶ coreApp.SessionOpRows (newest first)
//	                                         └▶ audit chain counts per session:
//	                                            WRITES = entries, NEW KEYS = distinct meta_keys rows (P3-RUNS-5)
package main

import (
	"context"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/logic/auditchain"
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
	dbPath, err := auditDBPath(oldProjectDir())
	if err != nil {
		return out, nil // no project here: the rows without counts
	}
	recs, err := auditRecords(ctx, coreApp, dbPath)
	if err != nil {
		return nil, err
	}
	sessionCounts(recs, out)
	return out, nil
}

// sessionCounts fills each row's Writes (its chain entries) and NewKeys
// (distinct meta_keys rows it wrote) from the audit chain (P3-RUNS-5).
func sessionCounts(recs []auditchain.Record, rows []opssession.Row) {
	writes := map[string]int{}
	keys := map[string]map[string]bool{}
	for _, r := range recs {
		if r.SessionID == "" {
			continue
		}
		writes[r.SessionID]++
		if r.TargetTable == schema.TableMetaKeys && r.TargetID != "" {
			if keys[r.SessionID] == nil {
				keys[r.SessionID] = map[string]bool{}
			}
			keys[r.SessionID][r.TargetID] = true
		}
	}
	for i := range rows {
		rows[i].Writes, rows[i].NewKeys = writes[rows[i].ID], len(keys[rows[i].ID])
	}
}
