package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"sync"

	// The pure-Go SQLite driver (no CGO); FTS5 and JSON1 are built in.
	_ "modernc.org/sqlite"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// driverName is the database/sql name modernc.org/sqlite registers.
const driverName = "sqlite"

// layoutVersion is PRAGMA user_version of a cache file this code writes.
// Bump it when the internal tables change shape: a file with another
// version is emptied and rebuilt (it is a cache), never migrated.
// 2: typed tables keyed by (_source, id); join tables filled from
// list-of-ref fields (O10, O11).
const layoutVersion = 2

// busyTimeoutMS is how long a writer waits for another process's lock
// before failing with SQLITE_BUSY.
const busyTimeoutMS = 5000

// Internal tables. `_rows` is the raw copy every typed table is projected
// from; row_refs and row_expanded are spec 4.5 and keep their spec names.
const (
	tblRows     = "_rows"
	tblCursors  = "_cursors"
	tblTables   = "_tables"
	tblRefs     = "row_refs"
	tblExpanded = "row_expanded"
)

// internalDDL creates the internal tables (idempotent).
var internalDDL = []string{
	`CREATE TABLE IF NOT EXISTS "` + tblRows + `" (source TEXT NOT NULL, tbl TEXT NOT NULL, id TEXT NOT NULL, hlc TEXT NOT NULL, deleted INTEGER NOT NULL, doc BLOB NOT NULL, PRIMARY KEY (source, tbl, id))`,
	`CREATE INDEX IF NOT EXISTS "ix__rows_id" ON "` + tblRows + `" (id)`,
	`CREATE INDEX IF NOT EXISTS "ix__rows_tbl" ON "` + tblRows + `" (tbl)`,
	`CREATE TABLE IF NOT EXISTS "` + tblCursors + `" (source TEXT PRIMARY KEY NOT NULL, cursor TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS "` + tblTables + `" (name TEXT PRIMARY KEY NOT NULL, fingerprint TEXT NOT NULL, objects TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS "` + tblRefs + `" (entity_table TEXT NOT NULL, entity_id TEXT NOT NULL, field TEXT NOT NULL, ref_id TEXT NOT NULL, PRIMARY KEY (entity_table, entity_id, field, ref_id))`,
	`CREATE INDEX IF NOT EXISTS "ix_row_refs_ref" ON "` + tblRefs + `" (ref_id)`,
	`CREATE TABLE IF NOT EXISTS "` + tblExpanded + `" (entity_table TEXT NOT NULL, entity_id TEXT NOT NULL, field TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY (entity_table, entity_id, field))`,
}

// Cache is one SQLite cache file. It is safe for concurrent use; writes are
// serialized (one connection), which is what a single-file SQLite cache
// does anyway.
//
// Invariant: st describes exactly the typed tables committed in the file;
// it is replaced only after the transaction that changed them commits.
type Cache struct {
	mu     sync.Mutex
	db     *sql.DB
	base   *schema.Registry
	exp    Expander
	st     state
	closed bool
}

var _ store.Cache = (*Cache)(nil)

// Open opens (creating if needed) the cache file at path. reg holds the
// built-in tables; kinds found in the file's rows are added to it. exp
// fills row_expanded and is required: a nil expander would leave row_refs
// silently empty, which a reader could not tell from "no references".
//
// On open every typed table whose definition changed since the file was
// written is dropped and refilled from `_rows`.
func Open(ctx context.Context, path string, reg *schema.Registry, exp Expander) (*Cache, error) {
	if reg == nil || exp == nil {
		return nil, errs.New(errs.InvalidInput, "sqlite cache: Open needs a registry and an expander")
	}
	dsn, err := fileDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, dbErr("open", err)
	}
	db.SetMaxOpenConns(1)
	c := &Cache{db: db, base: reg, exp: exp}
	if err := c.init(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return c, nil
}

// fileDSN builds the modernc DSN of a cache file (WAL, busy timeout,
// immediate transactions). The
// path is made absolute and URI-escaped, so a '?' or '#' in a folder name
// cannot be read as DSN options.
func fileDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errs.Wrap(errs.BadPath, "sqlite cache: bad path", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "busy_timeout("+strconv.Itoa(busyTimeoutMS)+")")
	// Every transaction here writes. A deferred one that reads first and
	// then writes fails at once with SQLITE_BUSY_SNAPSHOT (517) when another
	// process committed in between, and busy_timeout does not apply to
	// that; BEGIN IMMEDIATE takes the write lock up front, so concurrent
	// lore processes wait their turn instead (found by the old binary's
	// concurrent-writers end-to-end test).
	q.Add("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// init resets a file of another layout, creates the internal tables and
// brings every typed table in line with the definitions.
func (c *Cache) init(ctx context.Context) error {
	var ver int
	if err := c.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return dbErr("read layout version", err)
	}
	if ver != 0 && ver != layoutVersion {
		if err := resetFile(ctx, c.db); err != nil {
			return err
		}
	}
	var st state
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		for _, s := range append(slices.Clone(internalDDL), taskTreeDDL...) {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				return dbErr("create internal tables", err)
			}
		}
		if err := backfillTaskTree(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(layoutVersion)); err != nil {
			return dbErr("stamp layout version", err)
		}
		var err error
		if st, err = c.loadState(ctx, tx); err != nil {
			return err
		}
		return c.syncTables(ctx, tx, st)
	})
	if err != nil {
		return err
	}
	c.st = st
	return nil
}

// resetFile drops every object of a file written with another layout. Views
// go first and FTS shadow tables go with their virtual table.
func resetFile(ctx context.Context, db *sql.DB) error {
	for _, typ := range []string{"view", "table"} {
		for {
			var name string
			err := db.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type = ? AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY (sql LIKE 'CREATE VIRTUAL%') DESC LIMIT 1`, typ).Scan(&name)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return dbErr("list objects", err)
			}
			if _, err := db.ExecContext(ctx, "DROP "+typ+` IF EXISTS "`+escapeQuoted(name)+`"`); err != nil {
				return dbErr("drop "+typ, err)
			}
		}
	}
	return nil
}

// escapeQuoted doubles double quotes for a name read back from
// sqlite_master (any name a foreign layout made, so not ident-validated).
func escapeQuoted(s string) string {
	out := make([]byte, 0, len(s))
	for i := range len(s) {
		if s[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// inTx runs fn in one transaction, rolling back on error.
func (c *Cache) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr("begin", err)
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, ignoreDone(tx.Rollback()))
	}
	if err := tx.Commit(); err != nil {
		return dbErr("commit", err)
	}
	return nil
}

// ignoreDone drops the "already rolled back" error a cancelled context
// causes, so only the real failure is reported.
func ignoreDone(err error) error {
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

// usable fails after Close.
func (c *Cache) usable() error {
	if c.closed {
		return errs.Wrap(errs.Internal, "sqlite cache: closed", store.ErrClosed)
	}
	return nil
}

// Apply implements store.Cache: rows and cursor commit together or not at
// all. A row older (by HLC) than the copy already held for the same source
// is skipped, so a replayed or reordered read can never bring back an
// older version or a deleted row.
func (c *Cache) Apply(ctx context.Context, source store.Source, rows []store.Row, next store.Cursor) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return err
	}
	if source == "" {
		return errs.New(errs.InvalidInput, "sqlite cache: empty source")
	}
	st := c.st
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		applied := make([]store.Row, 0, len(rows))
		kindsChanged, tasksChanged := false, false
		for _, r := range rows {
			newer, err := keepRaw(ctx, tx, source, r)
			if err != nil {
				return err
			}
			if !newer {
				continue
			}
			applied = append(applied, r)
			tasksChanged = tasksChanged || r.Table == schema.TableTasks
			if r.Table == schema.TableKinds || r.Table == schema.TableKindFields {
				kindsChanged = true
			}
		}
		if kindsChanged {
			var err error
			if st, err = c.loadState(ctx, tx); err != nil {
				return err
			}
			if err := c.syncTables(ctx, tx, st); err != nil {
				return err
			}
		}
		for _, r := range applied {
			if err := c.project(ctx, tx, st, source, r); err != nil {
				return err
			}
		}
		// METADATA 3.13: quarantined metadata, counted and kept out of row_meta
		if err := refreshMetaQuarantine(ctx, tx, source, applied); err != nil {
			return err
		}
		if kindsChanged {
			// RES 3.13 / OV35-LASTGOOD: the last good definition per kind
			if err := refreshKindsGood(ctx, tx, source, applied); err != nil {
				return err
			}
		}
		if tasksChanged {
			if err := refreshTaskTree(ctx, tx); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO "`+tblCursors+`" (source, cursor) VALUES (?, ?) ON CONFLICT (source) DO UPDATE SET cursor = excluded.cursor`,
			string(source), string(next)); err != nil {
			return dbErr("record cursor", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.st = st
	return nil
}

// keepRaw stores r in `_rows` unless the held copy is newer; it reports
// whether r was stored.
func keepRaw(ctx context.Context, tx *sql.Tx, source store.Source, r store.Row) (bool, error) {
	if !r.Table.Valid() || r.ID.IsZero() || r.HLC.IsZero() || len(r.Doc) == 0 {
		return false, errs.New(errs.InvalidInput, "sqlite cache: row without table, id, hlc or document")
	}
	var held string
	err := tx.QueryRowContext(ctx, `SELECT hlc FROM "`+tblRows+`" WHERE source = ? AND tbl = ? AND id = ?`,
		string(source), r.Table.String(), r.ID.String()).Scan(&held)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, dbErr("read held row", err)
	default:
		h, err := hlc.Parse(held)
		if err != nil {
			return false, errs.Wrap(errs.DBCorrupt, "sqlite cache: held row has a bad hlc", err)
		}
		if r.HLC.Compare(h) < 0 {
			return false, nil
		}
	}
	deleted := 0
	if r.Deleted {
		deleted = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO "`+tblRows+`" (source, tbl, id, hlc, deleted, doc) VALUES (?, ?, ?, ?, ?, ?)`,
		string(source), r.Table.String(), r.ID.String(), r.HLC.String(), deleted, r.Doc); err != nil {
		return false, dbErr("store row", err)
	}
	return true, nil
}

// Cursor implements store.Cache.
func (c *Cache) Cursor(ctx context.Context, source store.Source) (store.Cursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return "", err
	}
	var cur string
	err := c.db.QueryRowContext(ctx, `SELECT cursor FROM "`+tblCursors+`" WHERE source = ?`, string(source)).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", dbErr("read cursor", err)
	}
	return store.Cursor(cur), nil
}

// Rebuild implements store.Cache: every row of source (raw and typed), its
// references and its cursor go, in one transaction; kinds that came only
// from source disappear with their tables.
func (c *Cache) Rebuild(ctx context.Context, source store.Source) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return err
	}
	var st state
	err := c.inTx(ctx, func(tx *sql.Tx) error {
		for _, name := range c.st.names() {
			p := c.st.plans[name]
			if err := clearSource(ctx, tx, p, source); err != nil {
				return err
			}
		}
		for _, q := range []string{
			`DELETE FROM "` + tblRows + `" WHERE source = ?`,
			`DELETE FROM "` + tblCursors + `" WHERE source = ?`,
			`DELETE FROM "` + tblMetaQuarantine + `" WHERE source = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, string(source)); err != nil {
				return dbErr("rebuild", err)
			}
		}
		if err := refreshTaskTree(ctx, tx); err != nil {
			return err
		}
		var err error
		if st, err = c.loadState(ctx, tx); err != nil {
			return err
		}
		return c.syncTables(ctx, tx, st)
	})
	if err != nil {
		return err
	}
	c.st = st
	return nil
}

// Close implements store.Cache; idempotent.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if err := c.db.Close(); err != nil {
		return dbErr("close", err)
	}
	return nil
}

// resolveRows is ResolveID's statement: every source holding a live row
// with the id.
const resolveRows = `SELECT source, tbl FROM "` + tblRows + `" WHERE id = ? AND deleted = 0 ORDER BY source, tbl`

// ResolveID finds the one source of this file holding a live row with id.
// In a combined cache (CFG3, several sources in one file) an id held by
// two sources is E_AMBIGUOUS_ID instead of whichever applied last; none is
// E_NOT_FOUND. LensHit.Source is the store.Source the row was applied as.
func (c *Cache) ResolveID(ctx context.Context, id ids.AnyID) (LensHit, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return LensHit{}, err
	}
	hits, err := resolveIn(c.db.QueryContext(ctx, resolveRows, id.String()))
	if err != nil {
		return LensHit{}, err
	}
	return oneHit(id, hits)
}

// Tables returns the typed tables the cache holds now (built-ins and
// kinds), sorted by name.
func (c *Cache) Tables() []row.Table {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st.names()
}

// dbErr wraps a driver error. The cause stays in the chain, so a cancelled
// context is still errors.Is(err, context.Canceled).
func dbErr(op string, err error) error {
	return errs.Wrap(errs.Internal, "sqlite cache: "+op, err)
}
