package wire_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/auditchain"
	"github.com/thesatellite-ai/lore/model/errs"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	"github.com/thesatellite-ai/lore/wire"
)

// withChainAudit installs cmd/lore's chain sink (StandaloneOptions does).
func withChainAudit(o *wire.Options) {
	o.Audit = wire.NewChainAudit(o.Home, o.RepoDir, time.Now)
}

// legacyLoreDB writes a v0.1.17-shaped lore.db with a two-entry chain
// (created_at a DATETIME, as ent stored it) at repo/.lore/lore.db.
func legacyLoreDB(t *testing.T, repo string) []auditchain.Record {
	t.Helper()
	a := auditchain.Record{ID: "aud_legacy1", CreatedAt: "2026-01-01T10:00:00Z", ActorID: "act_old", Action: "memories.write", TargetTable: "memories", TargetID: "mem_old"}
	b := auditchain.Record{ID: "aud_legacy2", CreatedAt: "2026-01-02T10:00:00Z", ActorID: "act_old", Action: "rules.external-write", Reason: "r", PrevLogHash: auditchain.Hash(a)}
	path := filepath.Join(repo, ".lore", "lore.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, `CREATE TABLE audit_logs (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME,
		acting_project_id TEXT, target_project_id TEXT, actor_id TEXT NOT NULL, action TEXT NOT NULL, target_table TEXT, target_id TEXT,
		override BOOL NOT NULL DEFAULT 0, before_hash TEXT, after_hash TEXT, prev_log_hash TEXT, reason TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []auditchain.Record{a, b} {
		at, perr := time.Parse(time.RFC3339, r.CreatedAt)
		if perr != nil {
			t.Fatal(perr)
		}
		var prev, reason, table, target sql.NullString
		prev = sql.NullString{String: r.PrevLogHash, Valid: r.PrevLogHash != ""}
		reason = sql.NullString{String: r.Reason, Valid: r.Reason != ""}
		table = sql.NullString{String: r.TargetTable, Valid: r.TargetTable != ""}
		target = sql.NullString{String: r.TargetID, Valid: r.TargetID != ""}
		if _, err = db.ExecContext(ctx, `INSERT INTO audit_logs (id, created_at, updated_at, actor_id, action, target_table, target_id, prev_log_hash, reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, at, at, r.ActorID, r.Action, table, target, prev, reason); err != nil {
			t.Fatal(err)
		}
	}
	return []auditchain.Record{a, b}
}

// TestChainAuditOnTheStandaloneSink: cmd/lore's sink imports a v0.1.17
// lore.db chain once, chains every repo write after it, `lore audit
// log|verify` read it (the import's external write counted), the
// append-only AuditFile is still written, and editing a chain row in the
// machine store fails verify with E_AUDIT_CHAIN_BROKEN (P3-LOCAL-7).
func TestChainAuditOnTheStandaloneSink(t *testing.T) {
	var legacy []auditchain.Record
	h := opstest.New(t, withChainAudit, func(o *wire.Options) { legacy = legacyLoreDB(t, o.RepoDir) })
	mem := h.ID(h.Must("memory.add", `{"body":"chained"}`))
	v := h.Must(opsaudit.OpVerify, `{}`)
	if txt := opstest.Text(v); !strings.Contains(txt, "audit chain intact") || !strings.Contains(txt, "1 change(s) were made outside lore") {
		t.Fatalf("verify = %s", txt)
	}
	log := h.JSON(h.Must(opsaudit.OpLog, `{"all":true}`))
	for _, want := range []string{legacy[0].ID, legacy[1].ID, mem, `"memories.write"`, `"2026-01-01T10:00:00Z"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit log lacks %s: %s", want, log)
		}
	}
	if strings.Count(log, legacy[0].ID) != 1 {
		t.Fatalf("legacy entry imported more than once: %s", log)
	}
	if b, err := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(wire.AuditFile))); err != nil || !strings.Contains(string(b), mem) {
		t.Fatalf("AuditFile = %q, %v", b, err)
	}
	ctx := context.Background()
	ch, err := h.App.AuditChain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := wire.AuditChainKey(filepath.Join(h.Repo, ".lore", "lore.db"))
	recs, err := ch.Records(ctx, key)
	if cerr := ch.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	if len(recs) < 3 || recs[0] != legacy[0] || recs[1] != legacy[1] || recs[2].PrevLogHash != auditchain.Hash(legacy[1]) {
		t.Fatalf("chain = %+v", recs)
	}
	// tamper: edit the first legacy entry in the machine store
	db, err := sql.Open("sqlite", filepath.Join(h.Home, filepath.FromSlash(wire.MachineStoreFile)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test handle
	if _, err = db.ExecContext(ctx, `UPDATE audit_chain SET action = 'forged' WHERE id = ?`, legacy[0].ID); err != nil {
		t.Fatal(err)
	}
	_, err = h.Run(opsaudit.OpVerify, `{}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.AuditChainBroken || !strings.Contains(e.Message, legacy[1].ID) || !strings.Contains(e.Hint, opsaudit.ChainFile) {
		t.Fatalf("tamper: err = %#v", err)
	}
}
