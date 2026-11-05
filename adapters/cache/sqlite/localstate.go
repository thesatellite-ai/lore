package sqlite

// localstate.go — the SQLite adapter of store/localstate (FA-OLD-LOCAL):
// one file per machine (<lore home>/state/local.db), one key → JSON table
// per localstate.Table, created at open (run-time DDL, no ent).

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// localDirMode is the mode of the state folder (owner only: P2-LINT-1).
const localDirMode = 0o750

// LocalState implements localstate.Store over one SQLite file.
type LocalState struct {
	db *sql.DB
	// sql holds each table's statements, built once at open from the
	// closed table list (identifiers validated by ident).
	sql map[localstate.Table]localSQL
}

// localSQL is the statement set of one machine-local table.
type localSQL struct {
	put, get, list, del string
}

// localStatements builds the statements of the table quoted as q.
func localStatements(q string) localSQL {
	return localSQL{
		put:  "INSERT INTO " + q + " (key, doc, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET doc = excluded.doc, updated_at = excluded.updated_at",
		get:  "SELECT doc, updated_at FROM " + q + " WHERE key = ?",
		list: "SELECT key, doc, updated_at FROM " + q + ` WHERE key LIKE ? ESCAPE '\' ORDER BY key`,
		del:  "DELETE FROM " + q + " WHERE key = ?",
	}
}

var _ localstate.Store = (*LocalState)(nil)

// OpenLocalState opens (creating it and its folder when missing) the
// machine-local state file at path and creates every localstate table.
func OpenLocalState(ctx context.Context, path string) (*LocalState, error) {
	if err := os.MkdirAll(filepath.Dir(path), localDirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "localstate: create "+filepath.Dir(path), err)
	}
	dsn, err := fileDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, dbErr("open local state", err)
	}
	db.SetMaxOpenConns(1)
	stmts := map[localstate.Table]localSQL{}
	for _, t := range localstate.Tables() {
		// Table names come from the closed localstate list, never input.
		q, ierr := ident(string(t))
		if ierr != nil {
			return nil, errors.Join(ierr, db.Close())
		}
		ddl := `CREATE TABLE IF NOT EXISTS ` + q + ` (key TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL)`
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return nil, errors.Join(dbErr("create "+string(t), err), db.Close())
		}
		stmts[t] = localStatements(q)
	}
	return &LocalState{db: db, sql: stmts}, nil
}

// Put implements localstate.Store.
func (l *LocalState) Put(ctx context.Context, t localstate.Table, key string, doc []byte) error {
	err := localstate.Check(t, key)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, l.sql[t].put,
		key, string(doc), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return dbErr("put "+string(t), err)
	}
	return nil
}

// Get implements localstate.Store.
func (l *LocalState) Get(ctx context.Context, t localstate.Table, key string) (localstate.Entry, bool, error) {
	err := localstate.Check(t, key)
	if err != nil {
		return localstate.Entry{}, false, err
	}
	var doc, at string
	err = l.db.QueryRowContext(ctx, l.sql[t].get, key).Scan(&doc, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return localstate.Entry{}, false, nil
	}
	if err != nil {
		return localstate.Entry{}, false, dbErr("get "+string(t), err)
	}
	ts, _ := time.Parse(time.RFC3339Nano, at) // written by Put; a bad value reads as zero time
	return localstate.Entry{Key: key, Doc: []byte(doc), UpdatedAt: ts}, true, nil
}

// List implements localstate.Store.
func (l *LocalState) List(ctx context.Context, t localstate.Table, prefix string) (out []localstate.Entry, err error) {
	if cerr := localstate.Check(t, "-"); cerr != nil {
		return nil, cerr
	}
	// LIKE with an escaped prefix: % and _ in a key are literal.
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	rows, err := l.db.QueryContext(ctx, l.sql[t].list, esc+"%")
	if err != nil {
		return nil, dbErr("list "+string(t), err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var k, doc, at string
		if serr := rows.Scan(&k, &doc, &at); serr != nil {
			return nil, dbErr("list "+string(t), serr)
		}
		ts, _ := time.Parse(time.RFC3339Nano, at) // written by Put
		out = append(out, localstate.Entry{Key: k, Doc: []byte(doc), UpdatedAt: ts})
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, dbErr("list "+string(t), rerr)
	}
	return out, nil
}

// Delete implements localstate.Store.
func (l *LocalState) Delete(ctx context.Context, t localstate.Table, key string) error {
	err := localstate.Check(t, key)
	if err != nil {
		return err
	}
	if _, err = l.db.ExecContext(ctx, l.sql[t].del, key); err != nil {
		return dbErr("delete "+string(t), err)
	}
	return nil
}

// Close implements localstate.Store.
func (l *LocalState) Close() error { return l.db.Close() }
