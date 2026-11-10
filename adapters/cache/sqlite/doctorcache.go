package sqlite

// doctorcache.go — the doctor check cache in the machine store file
// (<lore home>/state/machine.db, ONBOARDING_DESIGN 6.1: "Results are cached
// in machine.db for 60 seconds so the page and the CLI do not probe twice
// in a row"). One row per (setup, check, target): the result document, the
// time it was checked and the stamp of the inputs it was checked with.
//
//	lore doctor (process A) ─Put─▶ doctor_cache ◀─Get─ lore setup page (process B)
//	Get: a row whose stamp differs from the caller's (the setup, its key or
//	     a store changed since) is a miss, so a changed input is re-probed
//	Put: upsert, then drop rows older than the caller's horizon (the cache
//	     never grows past what a TTL could still serve)
//
// It is not an engine table on purpose: a cache entry is not a row (no id,
// no HLC, no audit), it is rebuilt by probing, and two processes writing it
// at once only need SQLite's own lock (BEGIN IMMEDIATE + busy_timeout,
// fileDSN), never an HLC check. The table is created at open, like the
// audit chain's tables beside machine_rows (no code generation).

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// The cache's statements: constants with bound parameters only (FB-DOC-1).
const (
	doctorCacheDDL = `CREATE TABLE IF NOT EXISTS doctor_cache (
		setup TEXT NOT NULL, check_id TEXT NOT NULL, target TEXT NOT NULL,
		stamp TEXT NOT NULL, checked_at TEXT NOT NULL, doc TEXT NOT NULL,
		PRIMARY KEY (setup, check_id, target))`
	doctorCacheGet = `SELECT stamp, checked_at, doc FROM doctor_cache WHERE setup = ? AND check_id = ? AND target = ?`
	doctorCachePut = `INSERT INTO doctor_cache (setup, check_id, target, stamp, checked_at, doc) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (setup, check_id, target) DO UPDATE SET stamp = excluded.stamp, checked_at = excluded.checked_at, doc = excluded.doc`
	// checked_at is RFC 3339 in UTC with a fixed width (doctorCacheTime), so
	// text order is time order and the prune compares strings.
	doctorCachePrune = `DELETE FROM doctor_cache WHERE checked_at < ?`
)

// doctorCacheTime is the stored time format: UTC, fixed-width nanoseconds,
// so two stored times compare as text in time order (the prune).
const doctorCacheTime = "2006-01-02T15:04:05.000000000Z"

// DoctorCacheKey names one cached check result.
type DoctorCacheKey struct {
	Setup, Check, Target string
}

// DoctorCacheEntry is one cached result: the caller's document (its JSON
// shape is the caller's), when it was checked and the input stamp.
type DoctorCacheEntry struct {
	Stamp     string
	CheckedAt time.Time
	Doc       []byte
}

// DoctorCache is the doctor_cache table of one machine store file. Safe
// for concurrent use; processes share it through SQLite's lock.
type DoctorCache struct {
	db     *sql.DB
	writes fileWrites // a failed write is explained (explainWrite)
}

// OpenDoctorCache opens (creating it and its folder when missing) the
// doctor cache table in the machine store file at path. The caller closes it.
func OpenDoctorCache(ctx context.Context, path string) (*DoctorCache, error) {
	if err := os.MkdirAll(filepath.Dir(path), localDirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "doctor cache: create "+filepath.Dir(path), err)
	}
	dsn, err := fileDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, dbErr("open doctor cache", err)
	}
	db.SetMaxOpenConns(1)
	if err := connectBusy(ctx, db); err != nil {
		return nil, errors.Join(explainOpen(path, dbErr("open doctor cache", err), FreeBytes), db.Close())
	}
	if err := execTx(ctx, db, doctorCacheDDL); err != nil {
		return nil, errors.Join(explainOpen(path, dbErr("create doctor cache table", err), FreeBytes), db.Close()) // E-FATES-3
	}
	return &DoctorCache{db: db, writes: writesTo(path)}, nil
}

// Close closes the handle.
func (d *DoctorCache) Close() error { return d.db.Close() }

// Get returns the entry at k; ok=false when there is none. Freshness and
// the stamp are the caller's to judge (it owns the TTL and the inputs).
func (d *DoctorCache) Get(ctx context.Context, k DoctorCacheKey) (DoctorCacheEntry, bool, error) {
	var stamp, at, doc string
	err := d.db.QueryRowContext(ctx, doctorCacheGet, k.Setup, k.Check, k.Target).Scan(&stamp, &at, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return DoctorCacheEntry{}, false, nil
	}
	if err != nil {
		return DoctorCacheEntry{}, false, dbErr("doctor cache: get", err)
	}
	ts, perr := time.Parse(doctorCacheTime, at)
	if perr != nil {
		// Not written by Put (a hand edit): no entry, so the check re-probes.
		return DoctorCacheEntry{}, false, nil
	}
	return DoctorCacheEntry{Stamp: stamp, CheckedAt: ts, Doc: []byte(doc)}, true, nil
}

// Put stores e at k (replacing what was there) and, in the same
// transaction, drops every entry checked before horizon (zero: none), so
// the table holds only what a TTL could still serve.
func (d *DoctorCache) Put(ctx context.Context, k DoctorCacheKey, e DoctorCacheEntry, horizon time.Time) (err error) {
	defer func() { err = d.writes.explain(err) }() // a quota or I/O failure names the file
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr("doctor cache: begin put", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err = tx.ExecContext(ctx, doctorCachePut, k.Setup, k.Check, k.Target, e.Stamp,
		e.CheckedAt.UTC().Format(doctorCacheTime), string(e.Doc)); err != nil {
		return dbErr("doctor cache: put", err)
	}
	if !horizon.IsZero() {
		if _, err = tx.ExecContext(ctx, doctorCachePrune, horizon.UTC().Format(doctorCacheTime)); err != nil {
			return dbErr("doctor cache: prune", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return dbErr("doctor cache: commit put", err)
	}
	return nil
}
