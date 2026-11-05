package sqlite

// machinestore.go — the store.Backend of the machine store
// (store.SourceMachine, PASS3 phase C, P3-LOCAL-1/2): this machine's
// machine-local rows (sessions, runs, render history, …) in one SQLite file
// at <lore home>/state/machine.db, never synced and never committed.
//
// Why a backend and not the key → JSON localstate port: these rows are
// real table rows (ids, HLC, audit fields, validation, secret scan, refs),
// so they go through the engine's one write path like every other row; the
// backend only stores the canonical documents and guards writes by HLC.
//
//	engine.put ──Write(changes)──▶ BEGIN IMMEDIATE
//	   every Expect checked against machine_rows.hlc ─ stale → rollback, ErrStale
//	   seq = max(seq)+1 for the batch ─▶ upsert every row ─▶ COMMIT
//	engine.Refresh ──Read(cursor = last seq seen)──▶ rows with seq > cursor
//
// Every statement is a constant with bound parameters (FB-DOC-1).

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// The machine store's layout: one table of row documents, one of purged ids.
const (
	machineRowsDDL   = `CREATE TABLE IF NOT EXISTS machine_rows (tbl TEXT NOT NULL, id TEXT NOT NULL, hlc TEXT NOT NULL, deleted INTEGER NOT NULL, doc BLOB NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY (tbl, id))`
	machineSeqIdx    = `CREATE INDEX IF NOT EXISTS machine_rows_seq ON machine_rows (seq)`
	machinePurgedDDL = `CREATE TABLE IF NOT EXISTS machine_purged (id TEXT PRIMARY KEY)`
	machineGet       = `SELECT hlc, deleted, doc FROM machine_rows WHERE tbl = ? AND id = ?`
	machineHLC       = `SELECT hlc FROM machine_rows WHERE tbl = ? AND id = ?`
	machineMaxSeq    = `SELECT COALESCE(MAX(seq), 0) FROM machine_rows`
	machineReadAfter = `SELECT tbl, id, hlc, deleted, doc FROM machine_rows WHERE seq > ? ORDER BY seq, tbl, id`
	machinePut       = `INSERT INTO machine_rows (tbl, id, hlc, deleted, doc, seq) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(tbl, id) DO UPDATE SET hlc = excluded.hlc, deleted = excluded.deleted, doc = excluded.doc, seq = excluded.seq`
	machinePurge     = `INSERT OR IGNORE INTO machine_purged (id) VALUES (?)`
	machinePurgedAll = `SELECT id FROM machine_purged`
)

// MachineStore is the machine store backend over one SQLite file. Safe for
// concurrent use; several processes share the file through SQLite's lock
// (BEGIN IMMEDIATE + busy_timeout, fileDSN).
type MachineStore struct {
	db       *sql.DB
	readOnly bool

	mu     sync.Mutex
	closed bool
}

var _ store.Backend = (*MachineStore)(nil)

// OpenMachineStore opens (creating it and its folder when missing) the
// machine store file at path. readOnly refuses every Write with
// store.ErrReadOnly.
func OpenMachineStore(ctx context.Context, path string, readOnly bool) (*MachineStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), localDirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "machine store: create "+filepath.Dir(path), err)
	}
	dsn, err := fileDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, dbErr("open machine store", err)
	}
	db.SetMaxOpenConns(1)
	if err := execTx(ctx, db, machineRowsDDL, machineSeqIdx, machinePurgedDDL); err != nil {
		return nil, errors.Join(dbErr("create machine store tables", err), db.Close())
	}
	return &MachineStore{db: db, readOnly: readOnly}, nil
}

// usable fails after Close or when ctx is done.
func (m *MachineStore) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errs.Wrap(errs.Internal, "machine store: closed", store.ErrClosed)
	}
	return nil
}

// Read implements store.Backend. The cursor is the decimal seq of the last
// batch seen; an unreadable cursor, or one past the file's last seq (a
// cursor of another file), means "from the beginning" (spec 5.2).
func (m *MachineStore) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	if err := m.usable(ctx); err != nil {
		return nil, "", err
	}
	var last int64
	if err := m.db.QueryRowContext(ctx, machineMaxSeq).Scan(&last); err != nil {
		return nil, "", dbErr("machine store seq", err)
	}
	after, perr := strconv.ParseInt(string(cursor), 10, 64)
	if perr != nil || after > last || after < 0 {
		after = 0
	}
	rows, err := m.db.QueryContext(ctx, machineReadAfter, after)
	if err != nil {
		return nil, "", dbErr("machine store read", err)
	}
	defer func() { _ = rows.Close() }()
	var out []store.Row
	for rows.Next() {
		var tbl, id, h string
		var deleted int
		var doc []byte
		if err := rows.Scan(&tbl, &id, &h, &deleted, &doc); err != nil {
			return nil, "", dbErr("machine store scan", err)
		}
		r, err := machineRow(tbl, id, h, deleted, doc)
		if err != nil {
			return nil, "", err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", dbErr("machine store read", err)
	}
	return out, store.Cursor(strconv.FormatInt(last, 10)), nil
}

// machineRow rebuilds a store.Row from its stored columns.
func machineRow(tbl, id, h string, deleted int, doc []byte) (store.Row, error) {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return store.Row{}, errs.Wrap(errs.Internal, "machine store: bad id "+id, err)
	}
	stamp, err := hlc.Parse(h)
	if err != nil {
		return store.Row{}, errs.Wrap(errs.Internal, "machine store: bad hlc of "+id, err)
	}
	return store.Row{Table: row.Table(tbl), ID: aid, HLC: stamp, Deleted: deleted != 0, Doc: doc}, nil
}

// Get implements store.Backend.
func (m *MachineStore) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	if err := m.usable(ctx); err != nil {
		return store.Row{}, err
	}
	var h string
	var deleted int
	var doc []byte
	err := m.db.QueryRowContext(ctx, machineGet, string(table), id.String()).Scan(&h, &deleted, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Row{}, store.ErrNotFound
	}
	if err != nil {
		return store.Row{}, dbErr("machine store get", err)
	}
	return machineRow(string(table), id.String(), h, deleted, doc)
}

// Write implements store.Backend: all or none in one IMMEDIATE transaction.
func (m *MachineStore) Write(ctx context.Context, changes []store.Change) error {
	return m.WritePurge(ctx, changes, nil)
}

// WritePurge implements the engine's Purger: changes like Write plus the
// purged ids, in the same transaction.
func (m *MachineStore) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) (err error) {
	if err = m.usable(ctx); err != nil {
		return err
	}
	if m.readOnly {
		return store.ErrReadOnly
	}
	seen := make(map[store.Key]struct{}, len(changes))
	for _, c := range changes {
		r := c.Row
		if !r.Table.Valid() || r.ID.IsZero() || r.HLC.IsZero() || len(r.Doc) == 0 {
			return errs.New(errs.InvalidInput, "machine store: change without table, id, hlc or document")
		}
		if _, dup := seen[r.Key()]; dup {
			return errs.Wrap(errs.InvalidInput, "machine store: batch", store.ErrBatch)
		}
		seen[r.Key()] = struct{}{}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr("machine store begin", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	for _, c := range changes {
		if err = checkExpect(ctx, tx, c); err != nil {
			return err
		}
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, machineMaxSeq).Scan(&seq); err != nil {
		return dbErr("machine store seq", err)
	}
	seq++
	for _, c := range changes {
		r := c.Row
		deleted := 0
		if r.Deleted {
			deleted = 1
		}
		if _, err = tx.ExecContext(ctx, machinePut, string(r.Table), r.ID.String(), r.HLC.String(), deleted, r.Doc, seq); err != nil {
			return dbErr("machine store put", err)
		}
	}
	for _, id := range purged {
		if _, err = tx.ExecContext(ctx, machinePurge, id.String()); err != nil {
			return dbErr("machine store purge", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return dbErr("machine store commit", err)
	}
	return nil
}

// checkExpect is the lost-update guard of one change: the zero Expect
// needs no stored row, any other the stored row's exact HLC.
func checkExpect(ctx context.Context, tx *sql.Tx, c store.Change) error {
	var h string
	err := tx.QueryRowContext(ctx, machineHLC, string(c.Row.Table), c.Row.ID.String()).Scan(&h)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbErr("machine store check", err)
	}
	if c.Expect.IsZero() {
		if exists {
			return store.ErrStale
		}
		return nil
	}
	if !exists {
		return store.ErrStale
	}
	cur, perr := hlc.Parse(h)
	if perr != nil || cur.Compare(c.Expect) != 0 {
		return store.ErrStale
	}
	return nil
}

// Purged implements the engine's Purger.
func (m *MachineStore) Purged(ctx context.Context) (map[string]bool, error) {
	if err := m.usable(ctx); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, machinePurgedAll)
	if err != nil {
		return nil, dbErr("machine store purged", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr("machine store purged", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("machine store purged", err)
	}
	return out, nil
}

// Sync implements store.Backend: the machine store has no remote, so
// nothing ever moves (P3-LOCAL-1: machine-local rows never travel).
func (m *MachineStore) Sync(ctx context.Context) (store.SyncReport, error) {
	if err := m.usable(ctx); err != nil {
		return store.SyncReport{}, err
	}
	return store.SyncReport{}, nil
}

// Capabilities implements store.Backend: none of the optional features.
func (m *MachineStore) Capabilities() store.Capabilities { return store.Capabilities{} }

// Close implements store.Backend; idempotent.
func (m *MachineStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.db.Close()
}
