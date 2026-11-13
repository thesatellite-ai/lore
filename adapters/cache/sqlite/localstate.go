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

// localDirMode is the mode of the state folder: owner only (P2-LINT-1), so
// the machine.db SQLite creates at the umask (0644) is never readable by
// the group. It is also the mode of a missing ~/.lore made on the way.
const localDirMode = 0o700

// All machine-local tables share one SQLite table keyed by (tbl, key), so
// every statement is a constant with bound parameters and no identifier is
// ever spliced into SQL (FB-DOC-1; fixes gosec G202). The localstate.Table
// name is a bound value, still checked against the closed list first.
const (
	localDDL  = `CREATE TABLE IF NOT EXISTS local_state (tbl TEXT NOT NULL, key TEXT NOT NULL, doc TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (tbl, key))`
	localPut  = `INSERT INTO local_state (tbl, key, doc, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(tbl, key) DO UPDATE SET doc = excluded.doc, updated_at = excluded.updated_at`
	localGet  = `SELECT doc, updated_at FROM local_state WHERE tbl = ? AND key = ?`
	localList = `SELECT key, doc, updated_at FROM local_state WHERE tbl = ? AND key LIKE ? ESCAPE '\' ORDER BY key`
	localDel  = `DELETE FROM local_state WHERE tbl = ? AND key = ?`
	// localHasTable asks whether a legacy per-table layout table exists.
	localHasTable = `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`
)

// legacyMoves copies the rows of the first layout (one SQLite table per
// localstate.Table, built before FB-DOC-1) into local_state and drops the
// old table. Constant statements per known table; a table added after
// FB-DOC-1 never had the old layout and needs no entry.
var legacyMoves = map[localstate.Table][2]string{
	localstate.Usage: {
		`INSERT OR IGNORE INTO local_state (tbl, key, doc, updated_at) SELECT 'row_usage', key, doc, updated_at FROM row_usage`,
		`DROP TABLE row_usage`,
	},
	localstate.SecretChecks: {
		`INSERT OR IGNORE INTO local_state (tbl, key, doc, updated_at) SELECT 'secret_checks', key, doc, updated_at FROM secret_checks`,
		`DROP TABLE secret_checks`,
	},
	localstate.TimeOutbox: {
		`INSERT OR IGNORE INTO local_state (tbl, key, doc, updated_at) SELECT 'time_outbox', key, doc, updated_at FROM time_outbox`,
		`DROP TABLE time_outbox`,
	},
}

// LocalState implements localstate.Store over one SQLite file.
type LocalState struct {
	db     *sql.DB
	writes fileWrites // a failed write is explained (explainWrite)
}

var _ localstate.Store = (*LocalState)(nil)

// OpenLocalState opens (creating it and its folder when missing) the
// machine-local state file at path and creates the shared table, moving
// rows of the old per-table layout into it once.
func OpenLocalState(ctx context.Context, path string) (*LocalState, error) {
	if err := os.MkdirAll(filepath.Dir(path), localDirMode); err != nil {
		return nil, errs.FileIO("create the local state folder", filepath.Dir(path), err)
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
	if err := connectBusy(ctx, db); err != nil {
		return nil, errors.Join(explainOpen(path, dbErr("open local state", err), FreeBytes), db.Close())
	}
	if _, err := db.ExecContext(ctx, localDDL); err != nil {
		return nil, errors.Join(explainOpen(path, dbErr("create local_state", err), FreeBytes), db.Close()) // E-FATES-3
	}
	if err := moveLegacyTables(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &LocalState{db: db, writes: writesTo(path)}, nil
}

// moveLegacyTables runs legacyMoves for each old table still present, each
// copy + drop in one transaction (a crash leaves the old table whole).
func moveLegacyTables(ctx context.Context, db *sql.DB) error {
	for _, t := range localstate.Tables() {
		mv, ok := legacyMoves[t]
		if !ok {
			continue
		}
		var one int
		err := db.QueryRowContext(ctx, localHasTable, string(t)).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return dbErr("check legacy "+string(t), err)
		}
		if err := execTx(ctx, db, mv[0], mv[1]); err != nil {
			return dbErr("move legacy "+string(t), err)
		}
	}
	return nil
}

// execTx runs constant statements in one transaction.
func execTx(ctx context.Context, db *sql.DB, stmts ...string) (err error) {
	// beginWrite, not BeginTx: several processes opening one fresh file run
	// this DDL at once, and SQLite's busy handler can starve a writer past
	// busy_timeout (busy.go)
	tx, err := beginWrite(ctx, db)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	for _, q := range stmts {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Put implements localstate.Store.
func (l *LocalState) Put(ctx context.Context, t localstate.Table, key string, doc []byte) error {
	err := localstate.Check(t, key)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, localPut,
		string(t), key, string(doc), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return l.writes.explain(dbErr("put "+string(t), err))
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
	err = l.db.QueryRowContext(ctx, localGet, string(t), key).Scan(&doc, &at)
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
	rows, err := l.db.QueryContext(ctx, localList, string(t), esc+"%")
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
	if _, err = l.db.ExecContext(ctx, localDel, string(t), key); err != nil {
		return l.writes.explain(dbErr("delete "+string(t), err))
	}
	return nil
}

// Close implements localstate.Store.
func (l *LocalState) Close() error { return l.db.Close() }
