// registry.go — per-entity FTS5 configuration.
//
// Phase 1 of search architecture (see docs/lore/SEARCH_INDEX_DESIGN.md):
// every text-bearing entity gets a multi-column FTS5 virtual table with
// per-column BM25 weights + porter stemming + diacritics fold.
//
// EnsureRegistrySchema creates the virtual tables, sync triggers, and
// backfills pre-existing rows. Idempotent — safe to call on every DB open.
//
// Adding a new entity:
//  1. Append a Config to Registry below
//  2. The next EnsureRegistrySchema call (any DB open) creates its FTS table
//  3. Search code reads from Registry — no callsite updates needed
package fts5

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Config describes how one entity's FTS5 virtual table is built.
type Config struct {
	// Entity is the singular noun used in command names ("memory", "task").
	Entity string

	// Table is the SQL table holding the entity (plural snake_case).
	Table string

	// Columns to index — order matters because weights below align by index.
	Columns []string

	// Weights for bm25(<table>_fts, w1, w2, ...). Higher = more impact on rank.
	// Convention: title/name=4, body=3, descriptive enums=1.
	Weights []float64

	// SchemaVersion is bumped whenever Columns or Weights changes. The
	// EnsureRegistrySchema pass compares this against the value last stored
	// in _aicoder_fts_schema_versions and drops+rebuilds the per-entity FTS
	// table when it sees a bump. Start at 1; bump when you edit the entry.
	SchemaVersion int
}

// FTSTable returns the virtual-table name for this entity (e.g. "memory_fts").
func (c Config) FTSTable() string {
	// Singular entity name + "_fts" matches the existing memory_fts pattern.
	return c.Entity + "_fts"
}

// Registry is the full set of FTS5-indexed entities. Keep alphabetized by
// Entity to make new-entity additions easier to review.
var Registry = []Config{
	{Entity: "architecturenote", Table: "architecture_notes",
		Columns: []string{"title", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "behaviour", Table: "behaviours",
		Columns: []string{"name", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "comment", Table: "comments",
		Columns: []string{"body"}, Weights: []float64{3}, SchemaVersion: 1},
	{Entity: "cookbookrecipe", Table: "cookbook_recipes",
		Columns: []string{"title", "body", "language"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "decision", Table: "decisions",
		Columns: []string{"title", "body", "status", "source_kind"}, Weights: []float64{4, 3, 1, 1}, SchemaVersion: 1},
	{Entity: "handoff", Table: "handoffs",
		Columns: []string{"body", "status_str"}, Weights: []float64{3, 1}, SchemaVersion: 1},
	{Entity: "hotfix", Table: "hotfixes",
		Columns: []string{"title", "body", "severity"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "incident", Table: "incidents",
		Columns: []string{"title", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "memory", Table: "memories",
		Columns: []string{"body", "source_ref", "kind", "source_kind"}, Weights: []float64{3, 1, 1, 1}, SchemaVersion: 1},
	{Entity: "mission", Table: "missions",
		Columns: []string{"title", "body", "status"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "pattern", Table: "patterns",
		Columns: []string{"title", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "plan", Table: "plans",
		Columns: []string{"title", "body", "status_str"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "playbook", Table: "playbooks",
		Columns: []string{"name", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "prompt", Table: "prompts",
		Columns: []string{"name", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "rule", Table: "rules",
		Columns: []string{"body", "severity", "activation", "source_kind"}, Weights: []float64{3, 1, 1, 1}, SchemaVersion: 1},
	{Entity: "snapshot", Table: "snapshots",
		Columns: []string{"title", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "suggestion", Table: "suggestions",
		Columns: []string{"title", "body", "status_str"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "task", Table: "tasks",
		Columns: []string{"title", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "tasklist", Table: "task_lists",
		Columns: []string{"title", "body", "status_str"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "tastepref", Table: "taste_prefs",
		Columns: []string{"body", "scope"}, Weights: []float64{3, 1}, SchemaVersion: 1},
	{Entity: "techdoc", Table: "tech_docs",
		Columns: []string{"name", "description", "base_url"}, Weights: []float64{4, 3, 1}, SchemaVersion: 1},
	{Entity: "workflow", Table: "workflows",
		Columns: []string{"name", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
	{Entity: "workspace", Table: "workspaces",
		Columns: []string{"name", "body"}, Weights: []float64{4, 3}, SchemaVersion: 1},
}

// FindConfig returns the Config for a given entity name (or empty + false).
func FindConfig(entity string) (Config, bool) {
	for _, c := range Registry {
		if c.Entity == entity {
			return c, true
		}
	}
	return Config{}, false
}

// EnsureRegistrySchema creates per-entity FTS5 virtual tables + triggers
// + backfills for every Config in Registry. Idempotent.
//
// Skips silently if FTS5 isn't compiled into SQLite (degraded mode).
// Each per-entity schema failure is collected but doesn't abort the loop —
// one entity with a missing source column shouldn't break the rest.
func EnsureRegistrySchema(ctx context.Context, db *sql.DB) error {
	if !Available(ctx, db) {
		// FTS5 missing — caller falls back to LIKE.
		return nil
	}
	// Tracking table for per-entity schema versions. Auto-rebuild fires
	// when registry SchemaVersion > stored version.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _aicoder_fts_schema_versions (
			entity TEXT PRIMARY KEY,
			version INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("fts5 versions table: %w", err)
	}

	var errs []string
	for _, c := range Registry {
		stored, _ := getStoredVersion(ctx, db, c.Entity)
		if stored > 0 && stored < c.SchemaVersion {
			// Schema bumped — drop+rebuild this entity's FTS.
			if err := dropEntitySchema(ctx, db, c); err != nil {
				errs = append(errs, fmt.Sprintf("%s drop: %v", c.Entity, err))
				continue
			}
		}
		if err := ensureEntitySchema(ctx, db, c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", c.Entity, err))
			continue
		}
		if err := setStoredVersion(ctx, db, c.Entity, c.SchemaVersion); err != nil {
			errs = append(errs, fmt.Sprintf("%s version: %v", c.Entity, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("fts5 registry: %s", strings.Join(errs, "; "))
	}
	return nil
}

func getStoredVersion(ctx context.Context, db *sql.DB, entity string) (int, error) {
	var v int
	err := db.QueryRowContext(ctx,
		`SELECT version FROM _aicoder_fts_schema_versions WHERE entity = ?`, entity).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return v, err
}

func setStoredVersion(ctx context.Context, db *sql.DB, entity string, version int) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO _aicoder_fts_schema_versions(entity, version) VALUES (?, ?)
		ON CONFLICT(entity) DO UPDATE SET version = excluded.version
	`, entity, version)
	return err
}

// dropEntitySchema removes the per-entity FTS virtual table + triggers
// so the next ensureEntitySchema call can rebuild from current Config.
// Used during schema-version bumps and from `lore search rebuild`.
func dropEntitySchema(ctx context.Context, db *sql.DB, c Config) error {
	ftsName := c.FTSTable()
	stmts := []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s_ai`, ftsName),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s_ad`, ftsName),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s_au`, ftsName),
		fmt.Sprintf(`DROP TABLE IF EXISTS %s`, ftsName),
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("drop %s: %w", ftsName, err)
		}
	}
	return nil
}

// RebuildEntity drops + recreates the per-entity FTS table and reindexes
// every source row. Exported for use by `lore search rebuild --kind=X`.
func RebuildEntity(ctx context.Context, db *sql.DB, entity string) error {
	cfg, ok := FindConfig(entity)
	if !ok {
		return fmt.Errorf("unknown entity %q", entity)
	}
	if err := dropEntitySchema(ctx, db, cfg); err != nil {
		return err
	}
	return ensureEntitySchema(ctx, db, cfg)
}

// RebuildAll drops + recreates every entity's FTS table.
// Used by `lore search rebuild` (no --kind flag).
func RebuildAll(ctx context.Context, db *sql.DB) error {
	if !Available(ctx, db) {
		return fmt.Errorf("fts5 not available")
	}
	var errs []string
	for _, c := range Registry {
		if err := dropEntitySchema(ctx, db, c); err != nil {
			errs = append(errs, fmt.Sprintf("%s drop: %v", c.Entity, err))
			continue
		}
		if err := ensureEntitySchema(ctx, db, c); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", c.Entity, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("rebuild: %s", strings.Join(errs, "; "))
	}
	return nil
}

// CountIndexed returns (fts_row_count, source_row_count) for an entity.
// Used by `lore search status` to report drift / health.
func CountIndexed(ctx context.Context, db *sql.DB, entity string) (int, int, error) {
	cfg, ok := FindConfig(entity)
	if !ok {
		return 0, 0, fmt.Errorf("unknown entity %q", entity)
	}
	var ftsCount, srcCount int
	_ = db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s`, cfg.FTSTable())).Scan(&ftsCount)
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s`, cfg.Table)).Scan(&srcCount); err != nil {
		return 0, 0, fmt.Errorf("count %s: %w", cfg.Table, err)
	}
	return ftsCount, srcCount, nil
}

// ensureEntitySchema is the per-entity worker. Three DDL pieces:
//  1. CREATE VIRTUAL TABLE <entity>_fts USING fts5(col1, col2, ...,
//     content='<table>', content_rowid='rowid', tokenize='porter ...')
//  2. Three triggers (AFTER INSERT / DELETE / UPDATE) keeping the FTS in sync
//  3. Backfill for any pre-existing rows not yet in FTS
func ensureEntitySchema(ctx context.Context, db *sql.DB, c Config) error {
	if len(c.Columns) == 0 {
		return fmt.Errorf("empty columns")
	}
	colList := strings.Join(c.Columns, ", ")
	ftsName := c.FTSTable()

	// 1. Create virtual table.
	createStmt := fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5(
			%s,
			content='%s',
			content_rowid='rowid',
			tokenize='porter unicode61 remove_diacritics 2'
		)`, ftsName, colList, c.Table)
	if _, err := db.ExecContext(ctx, createStmt); err != nil {
		return fmt.Errorf("create %s: %w", ftsName, err)
	}

	// 2. Sync triggers. For each col we need new.<col> and old.<col> refs.
	newCols := joinPrefixed("new", c.Columns)
	oldCols := joinPrefixed("old", c.Columns)
	triggers := []string{
		fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS %s_ai AFTER INSERT ON %s BEGIN
				INSERT INTO %s(rowid, %s) VALUES (new.rowid, %s);
			END`, ftsName, c.Table, ftsName, colList, newCols),
		fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS %s_ad AFTER DELETE ON %s BEGIN
				INSERT INTO %s(%s, rowid, %s) VALUES ('delete', old.rowid, %s);
			END`, ftsName, c.Table, ftsName, ftsName, colList, oldCols),
		fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS %s_au AFTER UPDATE ON %s BEGIN
				INSERT INTO %s(%s, rowid, %s) VALUES ('delete', old.rowid, %s);
				INSERT INTO %s(rowid, %s) VALUES (new.rowid, %s);
			END`, ftsName, c.Table, ftsName, ftsName, colList, oldCols, ftsName, colList, newCols),
	}
	for _, t := range triggers {
		if _, err := db.ExecContext(ctx, t); err != nil {
			return fmt.Errorf("create trigger on %s: %w", ftsName, err)
		}
	}

	// 3. Always run the FTS5 rebuild command to populate the index from
	// the source table.
	//
	// Why "always" instead of "only when row count differs":
	//   - For content='<table>' FTS5 tables, `SELECT COUNT(*) FROM <fts>`
	//     returns the source-table count (the index is "virtual" over
	//     the parent table), so a row-count compare can never detect a
	//     stale or empty index.
	//   - Schema-version-aware drop+rebuild and `search rebuild` already
	//     funnel through this code path, so rebuild here is the canonical
	//     primitive. The per-command path no longer calls this function
	//     (moved to `lore setup`), so the "always" cost is bounded
	//     to setup/init/rebuild — not every CLI invocation.
	//   - `INSERT INTO <fts>(<fts>) VALUES('rebuild')` is idempotent and
	//     fast (~ms per few thousand rows).
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s(%s) VALUES('rebuild')`, ftsName, ftsName)); err != nil {
		return fmt.Errorf("backfill %s: %w", ftsName, err)
	}
	return nil
}

// joinPrefixed returns "new.col1, new.col2, ..." (or "old.colN") for trigger
// payload generation.
func joinPrefixed(prefix string, cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + "." + c
	}
	return strings.Join(out, ", ")
}
