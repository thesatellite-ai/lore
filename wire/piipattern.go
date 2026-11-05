package wire

// piipattern.go — `lore pii-pattern list` (CLI_REFERENCE 2.25): the rows
// left in the v0.1.17 pii_patterns table of this checkout's lore.db, read
// only. Custom secret patterns are settings rows now (`lore secrets rule`);
// no scanner ever read this table, so the list is all that stays.

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	opspii "github.com/thesatellite-ai/lore/ops/piipattern"
)

// v017PIITable is the v0.1.17 ent table of custom PII patterns.
const v017PIITable = "pii_patterns"

// Statements of the read (constants: no row data in SQL text).
const (
	piiHasTable = "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	piiSelect   = "SELECT id, created_at, updated_at, name, regex, enabled, source_kind FROM " + v017PIITable + " ORDER BY name"
)

var _ opspii.Env = (*App)(nil)

// LegacyPIIPatterns implements opspii.Env. The checkout is the nearest
// folder at or above RepoDir holding .lore/lore.db (a Local operation opens
// no store, so the repo state is not read); none, or no table, is no rows.
// The database is opened read-only: v0.1.17 may still be using it.
func (a *App) LegacyPIIPatterns(ctx context.Context) ([]opspii.Pattern, error) {
	path, ok, err := findV017LoreDB(a.opts.RepoDir)
	if err != nil || !ok {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}).String()
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "pii-pattern: open "+v017LoreDB, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	var n int
	if err = db.QueryRowContext(ctx, piiHasTable, v017PIITable).Scan(&n); err != nil {
		return nil, errs.Wrap(errs.Internal, "pii-pattern: read "+v017LoreDB, err)
	}
	if n == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, piiSelect)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "pii-pattern: read the "+v017PIITable+" table", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []opspii.Pattern
	for rows.Next() {
		var p opspii.Pattern
		var created, updated, regex, source sql.NullString
		if err = rows.Scan(&p.ID, &created, &updated, &p.Name, &regex, &p.Enabled, &source); err != nil {
			return nil, errs.Wrap(errs.Internal, "pii-pattern: read a row", err)
		}
		p.CreatedAt, p.UpdatedAt, p.Regex, p.SourceKind = created.String, updated.String, regex.String, source.String
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "pii-pattern: read rows", err)
	}
	return out, nil
}

// findV017LoreDB walks up from dir to the first <folder>/.lore/lore.db.
func findV017LoreDB(dir string) (string, bool, error) {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return "", false, errs.Wrap(errs.Internal, "pii-pattern: resolve "+dir, err)
	}
	for {
		p := filepath.Join(cur, filepath.FromSlash(v017LoreDB))
		if st, serr := os.Stat(p); serr == nil && st.Mode().IsRegular() {
			return p, true, nil
		} else if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
			return "", false, errs.Wrap(errs.Internal, "pii-pattern: stat "+p, serr)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false, nil
		}
		cur = parent
	}
}
