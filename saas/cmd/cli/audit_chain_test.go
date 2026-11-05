package main

import (
	"context"
	"path/filepath"
	"testing"

	"saas/pkg/aicoder/audit"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/auditchain"
)

// auditLogsDDL is the v0.1.17 audit_logs columns the chain writer uses.
const auditLogsDDL = `CREATE TABLE audit_logs (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME,
	acting_project_id TEXT, target_project_id TEXT, actor_id TEXT NOT NULL, action TEXT NOT NULL,
	target_table TEXT, target_id TEXT, override BOOL NOT NULL DEFAULT 0, before_hash TEXT, after_hash TEXT,
	prev_log_hash TEXT, reason TEXT, session_id TEXT, confirmed BOOL NOT NULL DEFAULT 0)`

// TestLoreDBAuditMovesToTheMachineChain: a chain written by the v0.1.17
// writer (saas/pkg/aicoder/audit) in lore.db is copied once into the
// machine store chain, verifies there with the same hash rule, takes new
// appends linked to its head, and a second import (run twice) copies
// nothing (P3-LOCAL-7).
func TestLoreDBAuditMovesToTheMachineChain(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lore.db")
	db, err := openPrivateLoreDB(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, auditLogsDDL); err != nil {
		t.Fatal(err)
	}
	for _, e := range []audit.Entry{
		{ActorID: "act_a", Action: "memories.write", TargetTable: "memories", TargetID: "mem_1"},
		{ActorID: "act_a", Action: "rules.import", Reason: "why", Override: true, BeforeHash: "b", AfterHash: "c"},
		{ActorID: "act_b", Action: "tasks.write", TargetID: "tsk_1", SessionID: "ses_1", Confirmed: true},
	} {
		if err = audit.Append(ctx, db, e); err != nil {
			t.Fatal(err)
		}
	}
	if broken, n, verr := audit.Verify(ctx, db); verr != nil || broken != "" || n != 3 {
		t.Fatalf("v0.1.17 verify = %q, %d, %v", broken, n, verr)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	ch, err := sqlite.OpenAuditChain(ctx, filepath.Join(dir, "state", "machine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }() // test handle
	key := auditChainKey(dbPath)
	for range 2 {
		if err = importLoreDBAudit(ctx, ch, key, dbPath); err != nil {
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
	next, err := ch.Append(ctx, key, auditchain.Record{ActorID: "act_c", Action: "memories.delete"}, cliClock().Now())
	if err != nil || next.PrevLogHash != auditchain.Hash(recs[2]) {
		t.Fatalf("append after import = %+v, %v", next, err)
	}
	if broken, n, verr := ch.Verify(ctx, key); verr != nil || broken != "" || n != 4 {
		t.Fatalf("verify after append = %q, %d, %v", broken, n, verr)
	}
	// a project with no lore.db imports nothing, once
	empty := filepath.Join(dir, "none", "lore.db")
	if err = importLoreDBAudit(ctx, ch, auditChainKey(empty), empty); err != nil {
		t.Fatal(err)
	}
	if done, ierr := ch.Imported(ctx, auditChainKey(empty)); ierr != nil || !done {
		t.Fatalf("Imported(no lore.db) = %v, %v", done, ierr)
	}
}
