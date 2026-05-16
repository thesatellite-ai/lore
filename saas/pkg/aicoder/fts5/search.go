// search.go — generic per-entity FTS5 search.
//
// Returns rowids + ranked BM25 scores + snippets for a given Config.
// Entity-agnostic — callers do the ent.X.Query(...).Where(IDIn(...)) follow-up
// and any eager-loading of relations.
//
// Snippet column is FTS5's snippet() output with HTML-like markers around
// matched tokens; callers can strip or render as they wish.
package fts5

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EntityHit is one FTS5 match against a per-entity virtual table.
type EntityHit struct {
	ID      string  // entity primary key (e.g. "mem_018f...")
	RowID   int64   // raw SQLite rowid (used for vec0 joins in Phase 2)
	BM25    float64 // higher = better (sign flipped from SQLite's negative-leaning bm25)
	Snippet string  // FTS5 snippet() — bold markers around matched tokens
}

// SearchOptions threads optional filters into SearchEntity. All fields are
// optional — zero values mean "no filter on this dimension".
type SearchOptions struct {
	ProjectID       string   // scope to one project (most common case)
	RepoID          string   // optional repo filter (matches entity.repo_id)
	AllRepos        bool     // when true, repo filter is bypassed
	MasterOnly      bool     // when true, entity.repo_id IS NULL
	IncludeArchived bool     // when true, archived_at filter is bypassed
	Columns         []string // restrict MATCH to these FTS5 columns (subset of cfg.Columns)
	Limit           int      // default 20
}

// SearchEntity runs an FTS5 MATCH against cfg.FTSTable() and returns ranked
// hits. Falls back gracefully:
//   - If FTS5 isn't available, returns an empty slice + non-nil sentinel
//     error (caller can degrade to LIKE).
//   - If the query is invalid FTS5 syntax, returns SQLite's error verbatim
//     so the user sees what was wrong.
func SearchEntity(ctx context.Context, db *sql.DB, cfg Config, query string, opts SearchOptions) ([]EntityHit, error) {
	if !Available(ctx, db) {
		return nil, fmt.Errorf("fts5 not available")
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	ftsName := cfg.FTSTable()

	// Build column-scoped MATCH if --columns was passed. FTS5 syntax for
	// restricting MATCH to specific columns is: `{col1 col2}: <query>`.
	matchExpr := query
	if len(opts.Columns) > 0 {
		matchExpr = "{" + strings.Join(opts.Columns, " ") + "}: " + query
	}

	// Build bm25() with per-column weights from Config. SQLite expects one
	// arg per indexed column; mismatched count errors out, so default to
	// uniform 1.0 if Weights is wrong-length.
	weights := cfg.Weights
	if len(weights) != len(cfg.Columns) {
		weights = make([]float64, len(cfg.Columns))
		for i := range weights {
			weights[i] = 1.0
		}
	}
	weightArgs := make([]string, len(weights))
	for i, w := range weights {
		weightArgs[i] = fmt.Sprintf("%g", w)
	}

	// snippet() args: table, column-index (0-indexed, -1 = first matched),
	// start-marker, end-marker, ellipsis, max-tokens.
	q := fmt.Sprintf(`
		SELECT t.id, t.rowid,
		       bm25(%s, %s) AS rank,
		       snippet(%s, -1, '<b>', '</b>', '...', 32) AS snip
		FROM %s
		JOIN %s t ON t.rowid = %s.rowid
		WHERE %s MATCH ?
	`, ftsName, strings.Join(weightArgs, ", "),
		ftsName, ftsName, cfg.Table, ftsName, ftsName)

	args := []any{matchExpr}

	if opts.ProjectID != "" && entityHasProjectID(cfg) {
		q += ` AND t.project_id = ?`
		args = append(args, opts.ProjectID)
	}

	// Repo scope. Skip when entity doesn't have a repo_id column.
	if entityHasRepoID(cfg) && !opts.AllRepos {
		switch {
		case opts.MasterOnly:
			q += ` AND t.repo_id IS NULL`
		case opts.RepoID != "":
			q += ` AND (t.repo_id = ? OR t.repo_id IS NULL)`
			args = append(args, opts.RepoID)
		}
	}

	// Archive filter.
	if !opts.IncludeArchived && entityHasArchivedAt(cfg) {
		q += ` AND t.archived_at IS NULL`
	}

	q += ` ORDER BY rank LIMIT ?`
	args = append(args, opts.Limit)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("fts5 search %s: %w", ftsName, err)
	}
	defer rows.Close()

	out := make([]EntityHit, 0, opts.Limit)
	for rows.Next() {
		var h EntityHit
		var raw float64
		var snip sql.NullString
		if err := rows.Scan(&h.ID, &h.RowID, &raw, &snip); err != nil {
			return nil, fmt.Errorf("fts5 scan %s: %w", ftsName, err)
		}
		h.BM25 = -raw // SQLite returns negative; flip for "higher=better"
		if snip.Valid {
			h.Snippet = snip.String
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fts5 iter %s: %w", ftsName, err)
	}
	return out, nil
}

// entityHasProjectID returns false for polymorphic / global entities that
// don't carry a project_id column. Comment is the notable one — it's
// polymorphic (entity_table + entity_id) and lacks project scoping.
func entityHasProjectID(cfg Config) bool {
	switch cfg.Entity {
	case "comment":
		return false
	}
	return true
}

// entityHasRepoID — which entities have a repo_id column (per schema audit).
func entityHasRepoID(cfg Config) bool {
	switch cfg.Entity {
	case "memory", "rule", "decision", "hotfix", "pattern", "task":
		return true
	}
	return false
}

// entityHasArchivedAt — which entities use archived_at for soft-delete.
// Mirrors the archive_commands.go target list.
func entityHasArchivedAt(cfg Config) bool {
	switch cfg.Entity {
	case "memory", "rule", "decision", "hotfix", "pattern", "playbook",
		"prompt", "snapshot":
		return true
	}
	return false
}
