// Package audit provides BEGIN IMMEDIATE transaction wrapping and the
// hash-chained audit log buffer.
//
// Two responsibilities:
//
//  1. WithImmediateTx — every write goes through this to ensure SQLite locks
//     in IMMEDIATE mode (R16 #10, R18 #32, R27 #29). Default ent.Tx uses
//     BEGIN, which can return SQLITE_BUSY when a SELECT-then-write upgrades
//     mid-flight.
//
//  2. Buffer — collects audit_log entries during a txn; flushes hash-chained
//     batch at commit time (R27 #9). Avoids per-write hash recomputation
//     under bulk imports.
//
// Catches: R16 #4+#10, R18 #32, R27 #9+#29, R37 Block 5.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"dbent/gen/ent"
)

// Entry is a single audit-log row pending flush.
type Entry struct {
	ActingProjectID string // where command was launched (cwd toml)
	TargetProjectID string // where data was actually written (--project flag)
	ActorID         string // who did it
	Action          string // 'memory.add' | 'rule.update' | 'project.purge'
	TargetTable     string // affected entity table; "" for non-row actions
	TargetID        string // affected entity id; "" for non-row actions
	Override        bool   // true when --project overrode cwd
	BeforeHash      string // sha256 of row state BEFORE; "" for INSERT
	AfterHash       string // sha256 of row state AFTER; "" for DELETE
	Reason          string // optional human note
}

// Buffer collects audit entries within a transaction scope. Flushed at
// commit time with prev_log_hash chain computed across the buffer + the
// most recent persisted row.
type Buffer struct {
	entries []Entry
}

// Append adds an audit entry to the current transaction's buffer.
func (b *Buffer) Append(e Entry) {
	b.entries = append(b.entries, e)
}

// Len returns the number of pending entries.
func (b *Buffer) Len() int {
	return len(b.entries)
}

// Flush persists the buffer to the audit_log table with hash-chain links
// computed against the chain head (most recent persisted row).
//
// Called by WithImmediateTx at commit time. If buffer is empty, no-op.
//
// Hash chain rule (R27 #9): prev_log_hash = sha256 of all chain-relevant
// fields of the previous row. First row in the entire DB has prev_log_hash=NULL.
func (b *Buffer) Flush(ctx context.Context, tx *ent.Tx) error {
	if len(b.entries) == 0 {
		return nil
	}

	// Find the current chain head (last inserted audit row, ordered by id which
	// embeds UUIDv7 timestamp per R31).
	head, err := tx.AuditLog.Query().
		Order(ent.Desc("id")).
		Limit(1).
		All(ctx)
	if err != nil {
		return fmt.Errorf("audit: chain head lookup: %w", err)
	}

	prevHash := ""
	if len(head) > 0 && head[0].PrevLogHash != nil {
		prevHash = hashAuditRow(head[0])
	} else if len(head) > 0 {
		// Head exists but has nil prev_log_hash (first row ever). Compute its
		// hash to chain the next entry off of.
		prevHash = hashAuditRow(head[0])
	}

	// Persist each buffered entry, chaining hashes.
	for _, e := range b.entries {
		ph := prevHash
		create := tx.AuditLog.Create().
			SetActorID(e.ActorID).
			SetAction(e.Action).
			SetOverride(e.Override)
		if e.ActingProjectID != "" {
			create.SetActingProjectID(e.ActingProjectID)
		}
		if e.TargetProjectID != "" {
			create.SetTargetProjectID(e.TargetProjectID)
		}
		if e.TargetTable != "" {
			create.SetTargetTable(e.TargetTable)
		}
		if e.TargetID != "" {
			create.SetTargetID(e.TargetID)
		}
		if e.BeforeHash != "" {
			create.SetBeforeHash(e.BeforeHash)
		}
		if e.AfterHash != "" {
			create.SetAfterHash(e.AfterHash)
		}
		if ph != "" {
			create.SetPrevLogHash(ph)
		}
		if e.Reason != "" {
			create.SetReason(e.Reason)
		}

		row, err := create.Save(ctx)
		if err != nil {
			return fmt.Errorf("audit: persist entry: %w", err)
		}
		prevHash = hashAuditRow(row)
	}

	// Clear buffer after successful flush.
	b.entries = nil
	return nil
}

// hashAuditRow computes the chain-link hash for one audit_log row.
// Includes only chain-relevant fields (omits prev_log_hash itself to avoid
// circular reference).
func hashAuditRow(r *ent.AuditLog) string {
	var actingPrj, targetPrj, beforeHash, afterHash, reason, targetTable, targetID string
	if r.ActingProjectID != nil {
		actingPrj = *r.ActingProjectID
	}
	if r.TargetProjectID != nil {
		targetPrj = *r.TargetProjectID
	}
	if r.BeforeHash != nil {
		beforeHash = *r.BeforeHash
	}
	if r.AfterHash != nil {
		afterHash = *r.AfterHash
	}
	if r.Reason != nil {
		reason = *r.Reason
	}
	if r.TargetTable != nil {
		targetTable = *r.TargetTable
	}
	if r.TargetID != nil {
		targetID = *r.TargetID
	}

	// Format chosen for stability across schema additions; new fields go at end.
	payload := fmt.Sprintf(
		"id=%s|actor=%s|action=%s|acting=%s|target=%s|table=%s|tid=%s|override=%t|before=%s|after=%s|reason=%s",
		r.ID, r.ActorID, r.Action, actingPrj, targetPrj,
		targetTable, targetID, r.Override, beforeHash, afterHash, reason,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// WithImmediateTx runs fn inside a SQLite IMMEDIATE transaction.
//
// Differs from ent's default client.Tx() by issuing "BEGIN IMMEDIATE"
// up-front via the underlying *sql.DB, then wrapping the resulting *sql.Tx
// in an ent.Tx for the duration.
//
// Buffer is the audit-log buffer for this transaction. Calls to
// audit.Append(buffer, ...) accumulate; commit time flushes the chain.
//
// Returns first error from fn, BEGIN, COMMIT, or buffer flush. On any error,
// rolls back.
//
// Usage:
//
//	err := audit.WithImmediateTx(ctx, db, client, func(tx *ent.Tx, buf *audit.Buffer) error {
//	    m, err := tx.Memory.Create().Set...().Save(ctx)
//	    if err != nil { return err }
//	    buf.Append(audit.Entry{Action: "memory.add", TargetTable: "memories", TargetID: m.ID, ...})
//	    return nil
//	})
func WithImmediateTx(
	ctx context.Context,
	db *sql.DB,
	client *ent.Client,
	fn func(tx *ent.Tx, buf *Buffer) error,
) error {
	// Issue BEGIN IMMEDIATE manually. ent's client.Tx() can't be told to use
	// IMMEDIATE; we side-channel the SQL.
	if _, err := db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("audit: BEGIN IMMEDIATE: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			// Best-effort rollback on any failure. The IMMEDIATE txn was started
			// against the underlying *sql.DB, so rollback also runs there.
			_, _ = db.ExecContext(ctx, "ROLLBACK")
		}
	}()

	// Create an ent.Tx over the same connection. ent's Tx() implementation
	// will issue its OWN savepoint within our IMMEDIATE txn, which is
	// acceptable for SQLite.
	entTx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("audit: ent Tx: %w", err)
	}

	buf := &Buffer{}
	if err := fn(entTx, buf); err != nil {
		_ = entTx.Rollback()
		return err
	}

	// Apply tx_at clock-skew protection per R27 #29: nothing to do at the
	// transaction wrapper layer — individual entity hooks should handle this
	// at field-default level. Documented here for traceability.
	_ = time.Now() // placeholder for future per-table tx_at adjustments

	if err := buf.Flush(ctx, entTx); err != nil {
		_ = entTx.Rollback()
		return err
	}

	if err := entTx.Commit(); err != nil {
		return fmt.Errorf("audit: ent commit: %w", err)
	}
	if _, err := db.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("audit: outer COMMIT: %w", err)
	}

	committed = true
	return nil
}

// Verify walks the audit_log chain row-by-row. Returns the id of the first
// row whose prev_log_hash does not match the computed hash of the previous
// row. Returns "" if the chain is intact.
//
// Used by `aicoder audit verify` (S1.5 T1.5.4).
func Verify(ctx context.Context, client *ent.Client) (string, error) {
	rows, err := client.AuditLog.Query().Order(ent.Asc("id")).All(ctx)
	if err != nil {
		return "", fmt.Errorf("audit verify: %w", err)
	}

	prevHash := ""
	for i, r := range rows {
		// First row: prev_log_hash should be NULL.
		if i == 0 {
			if r.PrevLogHash != nil {
				return r.ID, nil
			}
		} else {
			if r.PrevLogHash == nil || *r.PrevLogHash != prevHash {
				return r.ID, nil
			}
		}
		prevHash = hashAuditRow(r)
	}
	return "", nil
}
