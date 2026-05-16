// Package fts5 wires SQLite FTS5 full-text-search with BM25 ranking over
// the memories.body column.
//
// Why FTS5 (vs LIKE):
//   - O(log n) inverted index instead of O(n) substring scan.
//   - BM25 ranking — orders hits by term-frequency / inverse-document-frequency.
//   - Phrase + boolean operators: "auth token", "redis OR cache", "-deprecated".
//
// Approach:
//   - One contentless virtual table `memory_fts` mirroring memories.body
//     keyed by memories.rowid (SQLite's implicit integer rowid alongside the
//     TEXT id PK).
//   - Triggers keep memory_fts in sync on INSERT/UPDATE/DELETE.
//   - Backfill on first migration so pre-existing rows index immediately.
//
// Idempotent: safe to call EnsureSchema() every run.
package fts5

import (
	"context"
	"database/sql"
	"fmt"
)

// EnsureSchema creates memory_fts + triggers + backfills if missing.
// Idempotent — call on every DB open after the main migration.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		// Contentless FTS5 mirror of memories.body. We use the explicit
		// 'memories' table as the content source so storage stays compact;
		// SQLite will look up the body when ranking. tokenize=unicode61
		// gives diacritics-folding + ASCII case-insensitivity for free.
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			body,
			content='memories',
			content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		)`,

		// Sync triggers. The 'delete' / 'delete-all' commands on contentless
		// FTS5 need the original body, which is why we mirror body in the
		// trigger payload.
		`CREATE TRIGGER IF NOT EXISTS memory_fts_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memory_fts(rowid, body) VALUES (new.rowid, new.body);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memory_fts_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memory_fts_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
			INSERT INTO memory_fts(rowid, body) VALUES (new.rowid, new.body);
		END`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("fts5 ensure-schema: %w", err)
		}
	}

	// Backfill any pre-existing memories rows that aren't yet in FTS.
	// Detect by comparing counts cheaply; only run INSERT-SELECT if mismatched.
	var memCount, ftsCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&memCount); err != nil {
		return fmt.Errorf("fts5 count memories: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_fts`).Scan(&ftsCount); err != nil {
		return fmt.Errorf("fts5 count memory_fts: %w", err)
	}
	if ftsCount < memCount {
		_, err := db.ExecContext(ctx, `
			INSERT INTO memory_fts(rowid, body)
			SELECT m.rowid, m.body FROM memories m
			WHERE m.rowid NOT IN (SELECT rowid FROM memory_fts)
		`)
		if err != nil {
			return fmt.Errorf("fts5 backfill: %w", err)
		}
	}
	return nil
}

// Hit is a single FTS5 result row in BM25 order.
type Hit struct {
	MemoryID string  // string PK (mem_<32-hex>)
	BM25     float64 // smaller = better in SQLite's bm25() (negative ranks; we flip sign)
}

// Search runs an FTS5 MATCH query against memory_fts, returning memory IDs
// in BM25 order. projectID scopes to a single project; pass "" for cross.
// Returns the IDs (caller does ent.Memory.Query().Where(IDIn(...))).
//
// Query syntax is FTS5 — supports phrase ("..."), boolean (AND/OR/NOT),
// prefix (foo*), and column filters. Validation is left to SQLite which
// surfaces a usable error message.
func Search(ctx context.Context, db *sql.DB, projectID, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `
		SELECT m.id, bm25(memory_fts) AS rank
		FROM memory_fts
		JOIN memories m ON m.rowid = memory_fts.rowid
		WHERE memory_fts MATCH ?
	`
	args := []any{query}
	if projectID != "" {
		q += ` AND m.project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("fts5 search: %w", err)
	}
	defer rows.Close()

	out := make([]Hit, 0, limit)
	for rows.Next() {
		var h Hit
		// SQLite returns bm25 as a negative-leaning float; lower is better.
		// We flip the sign so callers can sort "higher score = better"
		// if they prefer (we already return in correct order though).
		var raw float64
		if err := rows.Scan(&h.MemoryID, &raw); err != nil {
			return nil, fmt.Errorf("fts5 scan: %w", err)
		}
		h.BM25 = -raw
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fts5 iter: %w", err)
	}
	return out, nil
}

// Available probes whether the linked SQLite was built with FTS5 support.
// Some minimal SQLite builds (Alpine images, embedded) omit FTS5; the helper
// lets callers degrade gracefully to LIKE.
func Available(ctx context.Context, db *sql.DB) bool {
	_, err := db.ExecContext(ctx,
		`CREATE VIRTUAL TABLE IF NOT EXISTS _aicoder_fts_probe USING fts5(x)`)
	if err != nil {
		return false
	}
	_, _ = db.ExecContext(ctx, `DROP TABLE _aicoder_fts_probe`)
	return true
}
