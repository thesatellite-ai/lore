// Package audit keeps lore's hash-chained audit log (R16 #4, R27 #9,
// R37 Block 5): one row per change to a shared knowledge row, each row
// carrying the hash of the previous one, so deleting or editing a past
// entry breaks the chain and `lore audit verify` reports where.
//
// Entries are appended by the CLI's sync passes, which see every write to
// a synced table (see saas/pkg/aicoder/lsync, Options.OnChange) — lore's own
// writes, imports from .lore/data, and changes found in the DB that no lore
// command made. Append runs inside the caller's transaction, so the log and
// the change it describes commit together.
//
// Chain order is INSERTION order (rowid): ids are UUIDv7, which are not
// strictly ordered within one millisecond.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"saas/pkg/aicoder/ids"
)

// Querier is the subset of *sql.DB / *sql.Tx the log needs.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Entry is one audit-log row to append.
type Entry struct {
	ActingProjectID string `json:"acting_project_id,omitempty"` // where the command was launched
	TargetProjectID string `json:"target_project_id,omitempty"` // where data was written
	ActorID         string `json:"actor_id"`                    // who (actor id, or the resolved stable key)
	Action          string `json:"action"`                      // e.g. "memories.write", "rules.import"
	TargetTable     string `json:"target_table,omitempty"`      // affected table; "" for non-row actions
	TargetID        string `json:"target_id,omitempty"`         // affected row id; "" for non-row actions
	Override        bool   `json:"override"`                    // true when --project overrode the cwd project
	BeforeHash      string `json:"before_hash,omitempty"`       // content hash before; "" = row did not exist
	AfterHash       string `json:"after_hash,omitempty"`        // content hash after; "" = row was deleted
	Reason          string `json:"reason,omitempty"`            // optional note
	// SessionID is lore's sessions.id (never the tool's own id) of the
	// session that made the change; "" outside a session. Machine-local
	// (AGENT_IDENTITY_DESIGN 6.6). Hashed only when set (OQ10).
	SessionID string `json:"session_id,omitempty"`
	// Confirmed is true when the write followed --yes (8.1). Hashed only
	// when true, so every older entry keeps its fingerprint.
	Confirmed bool `json:"confirmed,omitempty"`
}

// Record is a persisted entry.
type Record struct {
	Entry
	ID          string `json:"id"`
	CreatedAt   string `json:"created_at"`
	PrevLogHash string `json:"prev_log_hash,omitempty"`
}

// auditTable is the ent table holding the log.
const auditTable = "audit_logs"

// selectCols lists the chain-relevant columns in hash order.
const selectCols = `id, actor_id, action, COALESCE(acting_project_id, ''), COALESCE(target_project_id, ''),
	COALESCE(target_table, ''), COALESCE(target_id, ''), override, COALESCE(before_hash, ''),
	COALESCE(after_hash, ''), COALESCE(prev_log_hash, ''), COALESCE(reason, ''), created_at`

// sessionCols are the two 0.1.18 columns (6.6). A lore.db built by
// v0.1.17 and not yet migrated (`lore setup`) lacks them; the log then
// reads them as empty and never writes them, so an old database keeps
// working (and verifying) unchanged.
const sessionCols = `, COALESCE(session_id, ''), confirmed`

// sessionColNames are the column names sessionCols reads.
var sessionColNames = [...]string{"session_id", "confirmed"}

// hasSessionCols reports whether audit_logs carries both sessionColNames.
func hasSessionCols(ctx context.Context, q Querier) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('`+auditTable+`') WHERE name IN (?, ?)`,
		sessionColNames[0], sessionColNames[1]).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("audit: read the columns: %w", err)
	}
	return n == len(sessionColNames), nil
}

// cols is the select list for this database.
func cols(withSession bool) string {
	if withSession {
		return selectCols + sessionCols
	}
	return selectCols
}

// Append adds e at the end of the chain. Call it inside the transaction
// that makes the change (immediate transactions serialise appenders, so two
// processes cannot fork the chain).
func Append(ctx context.Context, q Querier, e Entry) error {
	withSession, err := hasSessionCols(ctx, q)
	if err != nil {
		return err
	}
	if !withSession && (e.SessionID != "" || e.Confirmed) {
		// An un-migrated lore.db cannot hold the link; the change itself is
		// still logged (the fields are machine-local details, 6.6).
		e.SessionID, e.Confirmed = "", false
	}
	prev := ""
	head, err := scanOne(q.QueryRowContext(ctx, `SELECT `+cols(withSession)+` FROM `+auditTable+` ORDER BY rowid DESC LIMIT 1`), withSession)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("audit: chain head: %w", err)
	default:
		prev = hashRecord(head)
	}
	id, err := ids.New(ids.PrefixAuditLog)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	insert := `INSERT INTO ` + auditTable + `(id, created_at, updated_at, acting_project_id, target_project_id,
		actor_id, action, target_table, target_id, override, before_hash, after_hash, prev_log_hash, reason`
	args := []any{id, now, now, nullable(e.ActingProjectID), nullable(e.TargetProjectID), e.ActorID, e.Action,
		nullable(e.TargetTable), nullable(e.TargetID), e.Override, nullable(e.BeforeHash), nullable(e.AfterHash),
		nullable(prev), nullable(e.Reason)}
	values := `?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`
	if withSession {
		insert += `, session_id, confirmed`
		values += `, ?, ?`
		args = append(args, nullable(e.SessionID), e.Confirmed)
	}
	if _, err := q.ExecContext(ctx, insert+`) VALUES (`+values+`)`, args...); err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return nil
}

// Verify walks the chain in insertion order and returns the id of the first
// entry whose prev_log_hash does not match its predecessor ("" = intact).
func Verify(ctx context.Context, q Querier) (string, int, error) {
	withSession, err := hasSessionCols(ctx, q)
	if err != nil {
		return "", 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT `+cols(withSession)+` FROM `+auditTable+` ORDER BY rowid`)
	if err != nil {
		return "", 0, fmt.Errorf("audit: verify: %w", err)
	}
	defer rows.Close()
	prev, n := "", 0
	for rows.Next() {
		r, err := scanRows(rows, withSession)
		if err != nil {
			return "", n, err
		}
		if r.PrevLogHash != prev {
			return r.ID, n, nil
		}
		prev = hashRecord(r)
		n++
	}
	return "", n, rows.Err()
}

// List returns the newest entries first (limit <= 0 means all).
func List(ctx context.Context, q Querier, limit int) ([]Record, error) {
	withSession, err := hasSessionCols(ctx, q)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + cols(withSession) + ` FROM ` + auditTable + ` ORDER BY rowid DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRows(rows, withSession)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// hashRecord is the chain-link hash of one entry. The payload format is
// fixed: changing it invalidates every existing chain.
func hashRecord(r Record) string {
	payload := fmt.Sprintf(
		"id=%s|actor=%s|action=%s|acting=%s|target=%s|table=%s|tid=%s|override=%t|before=%s|after=%s|reason=%s",
		r.ID, r.ActorID, r.Action, r.ActingProjectID, r.TargetProjectID,
		r.TargetTable, r.TargetID, r.Override, r.BeforeHash, r.AfterHash, r.Reason)
	// OQ10: the 0.1.18 fields join the payload only when set, so an entry
	// without them hashes byte for byte as v0.1.17 did.
	if r.SessionID != "" {
		payload += "|session=" + r.SessionID
	}
	if r.Confirmed {
		payload += "|confirmed=true"
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

type scanner interface{ Scan(dest ...any) error }

func scanOne(row *sql.Row, withSession bool) (Record, error) { return scanInto(row, withSession) }

func scanRows(rows *sql.Rows, withSession bool) (Record, error) { return scanInto(rows, withSession) }

func scanInto(s scanner, withSession bool) (Record, error) {
	var r Record
	var created any
	dest := []any{&r.ID, &r.ActorID, &r.Action, &r.ActingProjectID, &r.TargetProjectID, &r.TargetTable,
		&r.TargetID, &r.Override, &r.BeforeHash, &r.AfterHash, &r.PrevLogHash, &r.Reason, &created}
	if withSession {
		dest = append(dest, &r.SessionID, &r.Confirmed)
	}
	err := s.Scan(dest...)
	switch v := created.(type) {
	case time.Time:
		r.CreatedAt = v.UTC().Format(time.RFC3339)
	case string:
		r.CreatedAt = v
	}
	return r, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
