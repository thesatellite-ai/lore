// Package cachedb is the cache of rows that do NOT belong to the checkout
// a command runs in: the linked store projects, the org's global project
// and the personal folders loaded for one code project (LORE_TEAM_SPEC F20,
// C18, ADR TEAM-019).
//
// Why a separate file, and why outside the checkout: the in-checkout
// .lore/lore.db is exported to .lore/data and committed, read by older
// binaries and copied by Docker builds and ZIPs. An internal row inside it
// would reach the client. So internal and personal rows live only in
// ~/.lore/cache/<project-id>.db, keyed by {entry, store commits}: when the
// org entry or any store commit changes, the whole cache is rebuilt, so
// switching org drops the old entry's rows (F20 meta key).
//
// Every row carries its source (`repo`, `store:<org>/<folder>`,
// `personal:<folder>`) and whether it came from a live ref. The schema
// version is stamped in PRAGMA user_version; a reader refuses a file
// written by a newer schema, and the application_id marks the file as a
// lore external cache so no other tool mistakes it for a lore.db.
package cachedb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dbent"
)

// SchemaVersion is this binary's cache schema. Bump it on any change; an
// older binary then refuses the file (ErrNewerSchema) and a newer one
// rebuilds it.
const SchemaVersion = 1

// applicationID marks a lore external cache ("LORX").
const applicationID = 0x4c4f5258

// File name pattern: <project-id>.db under the cache folder.
const fileExt = ".db"

// Meta keys.
const (
	metaKey      = "key"
	metaLastUsed = "last_used"
	metaBuilt    = "built"
)

// ErrNewerSchema: the file was written by a newer lore.
var ErrNewerSchema = errors.New("cachedb: cache written by a newer lore; upgrade lore")

// ErrNotACache: the file is not a lore external cache.
var ErrNotACache = errors.New("cachedb: not a lore external cache")

// Source kinds (the prefix of Row.Source).
const (
	SourceRepo     = "repo"
	SourceStore    = "store"
	SourcePersonal = "personal"
)

// StoreSource names a store project source: "store:<org>/<folder>".
func StoreSource(org, folder string) string { return SourceStore + ":" + org + "/" + folder }

// PersonalSource names a personal folder source: "personal:<folder>".
func PersonalSource(folder string) string { return SourcePersonal + ":" + folder }

// Row is one cached row.
type Row struct {
	// Source is repo | store:<org>/<folder> | personal:<folder>.
	Source string
	// Label is how lists show the source: [northwind/acme], [me].
	Label string
	// Table is the .lore/data table folder (memories, rules, …).
	Table string
	ID    string
	// Live is true when the row came from a live ref.
	Live bool
	// Title, Body and Status are extracted for listing and search; Doc is
	// the full canonical document.
	Title  string
	Body   string
	Status string
	Doc    json.RawMessage
}

// SourceFiles is one source's row files (path relative to .lore/data).
type SourceFiles struct {
	Source string
	Label  string
	// Files are branch data; LiveFiles (same layout) win over them.
	Files     map[string][]byte
	LiveFiles map[string][]byte
}

// Key identifies what a cache was built from: the org entry and the store
// commits read. Equal keys mean the cache is current.
type Key struct {
	Entry   string            `json:"entry"`
	Commits map[string]string `json:"commits"`
}

func (k Key) encode() string {
	b, _ := json.Marshal(k) // map keys are sorted by encoding/json; cannot fail for this type
	return string(b)
}

// Path returns <cacheDir>/<projectID>.db.
func Path(cacheDir, projectID string) string {
	return filepath.Join(cacheDir, projectID+fileExt)
}

// DB is an open cache.
type DB struct {
	db   *sql.DB
	path string
}

// Close closes the cache.
func (d *DB) Close() error { return d.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS rows (
	source TEXT NOT NULL,
	label TEXT NOT NULL,
	tbl TEXT NOT NULL,
	id TEXT NOT NULL,
	live INTEGER NOT NULL DEFAULT 0,
	title TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	doc TEXT NOT NULL,
	PRIMARY KEY (source, tbl, id)
);
CREATE INDEX IF NOT EXISTS rows_tbl ON rows (tbl);
`

// Open opens (creating when absent) the cache at path, refusing a file of
// a newer schema or a foreign file.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("cachedb: dir: %w", err)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("cachedb: %s is a symlink; refusing to open it", path)
	}
	db := dbent.InitDB(path)
	var ver, app int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		_ = db.Close() // the read error is the one to report
		return nil, fmt.Errorf("cachedb: read version: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&app); err != nil {
		_ = db.Close() // the read error is the one to report
		return nil, fmt.Errorf("cachedb: read application id: %w", err)
	}
	switch {
	case ver == 0 && app == 0:
		// new file (or an empty one): initialise below
	case app != applicationID:
		_ = db.Close() // refusing the file
		return nil, fmt.Errorf("%w: %s", ErrNotACache, path)
	case ver > SchemaVersion:
		_ = db.Close() // refusing the file
		return nil, fmt.Errorf("%w (file v%d, this lore v%d): %s", ErrNewerSchema, ver, SchemaVersion, path)
	case ver < SchemaVersion:
		// an older layout: drop and rebuild (it is only a cache)
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS rows; DROP TABLE IF EXISTS meta;"); err != nil {
			_ = db.Close() // the drop error is the one to report
			return nil, fmt.Errorf("cachedb: reset old cache: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close() // the schema error is the one to report
		return nil, fmt.Errorf("cachedb: schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d; PRAGMA user_version = %d;", applicationID, SchemaVersion)); err != nil {
		_ = db.Close() // the pragma error is the one to report
		return nil, fmt.Errorf("cachedb: stamp version: %w", err)
	}
	return &DB{db: db, path: path}, nil
}

// Current reports whether the cache was built from key.
func (d *DB) Current(ctx context.Context, key Key) (bool, error) {
	v, ok, err := d.meta(ctx, metaKey)
	if err != nil || !ok {
		return false, err
	}
	return v == key.encode(), nil
}

func (d *DB) meta(ctx context.Context, k string) (string, bool, error) {
	var v string
	err := d.db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = ?", k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("cachedb: read meta: %w", err)
	}
	return v, true, nil
}

// Built reports whether the cache was ever built (holds a key).
func (d *DB) Built(ctx context.Context) (bool, error) {
	_, ok, err := d.meta(ctx, metaKey)
	return ok, err
}

// Rebuild replaces every row with the given sources and records key. One
// transaction: a reader sees the old cache or the new one, never a mix of
// two entries (orgs never mix, fixed rule 12).
func (d *DB) Rebuild(ctx context.Context, key Key, sources []SourceFiles, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cachedb: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	if _, err := tx.ExecContext(ctx, "DELETE FROM rows"); err != nil {
		return fmt.Errorf("cachedb: clear: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO rows (source, label, tbl, id, live, title, body, status, doc) VALUES (?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return fmt.Errorf("cachedb: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }() // closed with the transaction anyway
	for _, s := range sources {
		for _, r := range RowsOf(s) {
			if _, err := stmt.ExecContext(ctx, r.Source, r.Label, r.Table, r.ID, boolInt(r.Live), r.Title, r.Body, r.Status, string(r.Doc)); err != nil {
				return fmt.Errorf("cachedb: insert %s/%s: %w", r.Table, r.ID, err)
			}
		}
	}
	for k, v := range map[string]string{metaKey: key.encode(), metaBuilt: now.UTC().Format(time.RFC3339Nano), metaLastUsed: now.UTC().Format(time.RFC3339Nano)} {
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO meta (k, v) VALUES (?, ?)", k, v); err != nil {
			return fmt.Errorf("cachedb: meta: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cachedb: commit: %w", err)
	}
	return nil
}

// Touch records that the cache was used now (for `lore cache prune`).
func (d *DB) Touch(ctx context.Context, now time.Time) error {
	if _, err := d.db.ExecContext(ctx, "INSERT OR REPLACE INTO meta (k, v) VALUES (?, ?)", metaLastUsed, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("cachedb: touch: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RowsOf turns one source's files into rows: every <table>/<id>.json, with
// a live file replacing the branch file of the same path (live wins, fixed
// rule 4) and a live tombstone (`_live.deleted`) hiding it; a 0.1.18
// tombstone file (`_deleted: true`) is no row either. Files starting
// with "_" (_meta.json, _purged.json) are not rows. Sorted.
func RowsOf(s SourceFiles) []Row {
	docs := map[string][]byte{}
	live := map[string]bool{}
	for p, b := range s.Files {
		docs[p] = b
	}
	for p, b := range s.LiveFiles {
		docs[p] = b
		live[p] = true
	}
	var out []Row
	for p, b := range docs {
		table, file, ok := strings.Cut(p, "/")
		if !ok || strings.Contains(file, "/") || !strings.HasSuffix(file, ".json") || strings.HasPrefix(table, "_") {
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(b, &doc); err != nil {
			continue // a broken file is reported by the owner's sync, not cached
		}
		// A 0.1.18 row file (_v 2) that is a tombstone carries no row: its
		// id must not count as internal content (the row was deleted or
		// moved away with `lore move`, which keeps the id).
		if del, ok := doc["_deleted"]; ok {
			var d bool
			if json.Unmarshal(del, &d) == nil && d {
				continue
			}
		}
		if lv, ok := doc["_live"]; ok {
			var l struct {
				Deleted bool `json:"deleted"`
			}
			if json.Unmarshal(lv, &l) == nil && l.Deleted {
				continue
			}
		}
		r := Row{Source: s.Source, Label: s.Label, Table: table, ID: strings.TrimSuffix(file, ".json"), Live: live[p], Doc: json.RawMessage(b)}
		r.Title = firstString(doc, "title", "name", "summary")
		r.Body = firstString(doc, "body", "content", "description", "text")
		r.Status = firstString(doc, "status")
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func firstString(doc map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := doc[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

// Query selects cached rows.
type Query struct {
	// Table filters by table ("" = all).
	Table string
	// Text keeps rows whose title or body contains it (case-insensitive).
	Text string
	// Sources keeps only these sources (nil = all).
	Sources []string
}

// Rows returns the rows matching q, ordered by source then id.
func (d *DB) Rows(ctx context.Context, q Query) ([]Row, error) {
	sqlq := "SELECT source, label, tbl, id, live, title, body, status, doc FROM rows WHERE 1=1"
	var args []any
	if q.Table != "" {
		sqlq += " AND tbl = ?"
		args = append(args, q.Table)
	}
	if q.Text != "" {
		sqlq += " AND (instr(lower(title), ?) > 0 OR instr(lower(body), ?) > 0)"
		t := strings.ToLower(q.Text)
		args = append(args, t, t)
	}
	if len(q.Sources) > 0 {
		sqlq += " AND source IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Sources)), ",") + ")"
		for _, s := range q.Sources {
			args = append(args, s)
		}
	}
	sqlq += " ORDER BY source, tbl, id"
	rs, err := d.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("cachedb: query: %w", err)
	}
	defer func() { _ = rs.Close() }() // read-only cursor
	var out []Row
	for rs.Next() {
		var r Row
		var live int
		var doc string
		if err := rs.Scan(&r.Source, &r.Label, &r.Table, &r.ID, &live, &r.Title, &r.Body, &r.Status, &doc); err != nil {
			return nil, fmt.Errorf("cachedb: scan: %w", err)
		}
		r.Live, r.Doc = live == 1, json.RawMessage(doc)
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("cachedb: rows: %w", err)
	}
	return out, nil
}

// PruneResult lists what Prune removed.
type PruneResult struct {
	Removed []string
	Kept    int
}

// Prune removes caches under cacheDir not used for longer than maxIdle
// (side effect 40), and for each the matching render file in renderDir
// (<project-id>.md). A cache that cannot be read is removed too: it is
// only a cache. Only <prj_…>.db files are considered.
func Prune(ctx context.Context, cacheDir, renderDir string, now time.Time, maxIdle time.Duration) (PruneResult, error) {
	entries, err := os.ReadDir(cacheDir)
	if errors.Is(err, os.ErrNotExist) {
		return PruneResult{}, nil
	}
	if err != nil {
		return PruneResult{}, fmt.Errorf("cachedb: list %s: %w", cacheDir, err)
	}
	var res PruneResult
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, projectPrefix) || !strings.HasSuffix(name, fileExt) {
			continue
		}
		pid := strings.TrimSuffix(name, fileExt)
		path := filepath.Join(cacheDir, name)
		last, ok := lastUsed(ctx, path)
		if ok && now.Sub(last) <= maxIdle {
			res.Kept++
			continue
		}
		for _, p := range []string{path, path + "-wal", path + "-shm", filepath.Join(renderDir, pid+".md")} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, fmt.Errorf("cachedb: prune %s: %w", p, err)
			}
		}
		res.Removed = append(res.Removed, pid)
	}
	return res, nil
}

// projectPrefix is the id prefix of project ids (ids.PrefixProject + "_").
const projectPrefix = "prj_"

func lastUsed(ctx context.Context, path string) (time.Time, bool) {
	d, err := Open(ctx, path)
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = d.Close() }() // read-only use
	v, ok, err := d.meta(ctx, metaLastUsed)
	if err != nil || !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
