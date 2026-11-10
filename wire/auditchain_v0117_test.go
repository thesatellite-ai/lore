package wire_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/auditchain"
	"github.com/thesatellite-ai/lore/wire"
)

// v0117AuditRows are three audit_logs rows exactly as the v0.1.17 writer
// (saas/pkg/aicoder/audit.Append over modernc sqlite, deleted in phase E)
// stored them, captured byte for byte before the deletion: created_at is
// time.Time's String form, nullable columns are NULL when empty, the
// session_id and confirmed columns are present. They pin that the move to
// the machine chain reads real old rows, not rows shaped by the new code.
var v0117AuditRows = []string{
	`INSERT INTO audit_logs VALUES ('aud_01a1acb6edfa7cb48f8a07278c118a62', '2026-11-05 17:17:12.058834748 +0000 UTC', '2026-11-05 17:17:12.058834748 +0000 UTC', NULL, NULL, 'act_a', 'memories.write', 'memories', 'mem_1', 0, NULL, NULL, NULL, NULL, NULL, 0)`,
	`INSERT INTO audit_logs VALUES ('aud_01a1acb6edfb70ada2536027a4caccc3', '2026-11-05 17:17:12.059045586 +0000 UTC', '2026-11-05 17:17:12.059045586 +0000 UTC', NULL, NULL, 'act_a', 'rules.import', NULL, NULL, 1, 'b', 'c', '4795f93f99d6118d8177599e203196051ac95769e0ab9668934f40ae605d7237', 'why', NULL, 0)`,
	`INSERT INTO audit_logs VALUES ('aud_01a1acb6edfb7329a94441b4a03353d5', '2026-11-05 17:17:12.059208001 +0000 UTC', '2026-11-05 17:17:12.059208001 +0000 UTC', NULL, NULL, 'act_b', 'tasks.write', NULL, 'tsk_1', 0, NULL, NULL, 'fd66b43a254732b2b861eef0c1a260ed0130cf94d1d94f15785fdad507a3f7e8', NULL, 'ses_1', 1)`,
}

// v0117AuditDDL is the v0.1.17 audit_logs table with the session columns.
const v0117AuditDDL = `CREATE TABLE audit_logs (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME,
	acting_project_id TEXT, target_project_id TEXT, actor_id TEXT NOT NULL, action TEXT NOT NULL,
	target_table TEXT, target_id TEXT, override BOOL NOT NULL DEFAULT 0, before_hash TEXT, after_hash TEXT,
	prev_log_hash TEXT, reason TEXT, session_id TEXT, confirmed BOOL NOT NULL DEFAULT 0)`

// TestLoreDBAuditMovesToTheMachineChain: a chain written by the v0.1.17
// writer in lore.db is copied once into the machine store chain, verifies
// there with the same hash rule, takes new appends linked to its head, a
// second import (run twice) copies nothing, and a project with no lore.db
// is marked imported with nothing copied (P3-LOCAL-7; ported from
// saas/cmd/cli/audit_chain_test.go in phase E).
func TestLoreDBAuditMovesToTheMachineChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lore.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range append([]string{v0117AuditDDL}, v0117AuditRows...) {
		if _, err = db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	ch, err := sqlite.OpenAuditChain(ctx, filepath.Join(dir, "state", "machine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }() // test handle
	key := wire.AuditChainKey(dbPath)
	now := time.Now()
	for range 2 {
		if err = wire.ImportLoreDBAudit(ctx, ch, key, dbPath, now); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := ch.Records(ctx, key)
	if err != nil || len(recs) != 3 || recs[0].TargetID != "mem_1" || !recs[1].Override || recs[2].SessionID != "ses_1" || !recs[2].Confirmed {
		t.Fatalf("imported = %+v, %v", recs, err)
	}
	if broken, n := auditchain.Verify(recs); broken != "" || n != 3 {
		t.Fatalf("machine chain verify = %q, %d", broken, n)
	}
	next, err := ch.Append(ctx, key, auditchain.Record{ActorID: "act_c", Action: "memories.delete"}, now)
	if err != nil || next.PrevLogHash != auditchain.Hash(recs[2]) {
		t.Fatalf("append after import = %+v, %v", next, err)
	}
	if broken, n, verr := ch.Verify(ctx, key); verr != nil || broken != "" || n != 4 {
		t.Fatalf("verify after append = %q, %d, %v", broken, n, verr)
	}
	empty := filepath.Join(dir, "none", "lore.db")
	if err = wire.ImportLoreDBAudit(ctx, ch, wire.AuditChainKey(empty), empty, now); err != nil {
		t.Fatal(err)
	}
	if done, ierr := ch.Imported(ctx, wire.AuditChainKey(empty)); ierr != nil || !done {
		t.Fatalf("Imported(no lore.db) = %v, %v", done, ierr)
	}
}
