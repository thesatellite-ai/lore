package sqlite

// auditchain.go — the hash-chained audit log in the machine store file
// (<lore home>/state/machine.db, P3-LOCAL-7): the v0.1.17 lore.db
// audit_logs chain moved unchanged (same columns, same hash, same verify),
// one chain per key (the old binary keys it by the lore.db path whose
// chain it continues, so every checkout keeps its own chain as in
// v0.1.17). Never synced: the file never leaves the machine.
//
//	Import(chain, source, recs) ─▶ once per chain (marker row): the v0.1.17 rows, verbatim, in order
//	Append(chain, e)            ─▶ BEGIN IMMEDIATE · head · prev = auditchain.Hash(head) · insert pos+1
//	Records(chain)              ─▶ oldest first (pos order)
//	Verify(chain)               ─▶ auditchain.Verify(Records) + the head anchor
//
// The head anchor (P3-AUDIT-3): the links prove every entry but the newest,
// which nothing after it hashes, and they cannot show the newest entries
// were cut off. So each Append/Import also moves audit_chain_head (pos, id
// and hash of the newest entry), a separate table written in the same
// transaction. Verify then needs the newest row to be at the anchored pos
// with the anchored hash: an edited newest entry, a deleted one (tail
// truncation) and a deleted anchor are each reported as broken. A write
// whose chain no longer matches its anchor never moves the anchor, so a
// tamper stays reported after later writes (it is never laundered into a
// fresh anchor). A chain written before the anchor existed (unreleased
// 0.1.18 builds only; v0.1.17 rows arrive through Import, which anchors)
// reports as broken at its newest entry. This guards against edits of the
// machine store file with a SQLite tool; it is not a defence against
// someone who rewrites both tables together.
//
// It is not an engine table on purpose: the chain needs the head read and
// the insert in one write transaction (two processes must never fork it)
// and a strict insertion order, which an engine row (UUIDv7 ids, no lock
// across read and write) cannot give; and an audit row written through the
// engine would itself be audited. The table lives in the machine store's
// file beside machine_rows, so phase E deletes ent with nothing left in
// lore.db.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/logic/auditchain"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// auditTag types the ids the chain makes (the v0.1.17 "aud" prefix).
type auditTag struct{}

// Prefix implements ids.Tag.
func (auditTag) Prefix() ids.Prefix { return ids.PrefixAuditLog }

// The chain's statements: constants with bound parameters only.
const (
	auditDDL = `CREATE TABLE IF NOT EXISTS audit_chain (
		chain TEXT NOT NULL, pos INTEGER NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL,
		acting_project_id TEXT NOT NULL DEFAULT '', target_project_id TEXT NOT NULL DEFAULT '',
		actor_id TEXT NOT NULL, action TEXT NOT NULL, target_table TEXT NOT NULL DEFAULT '', target_id TEXT NOT NULL DEFAULT '',
		override INTEGER NOT NULL DEFAULT 0, before_hash TEXT NOT NULL DEFAULT '', after_hash TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '', confirmed INTEGER NOT NULL DEFAULT 0,
		prev_log_hash TEXT NOT NULL DEFAULT '', PRIMARY KEY (chain, pos), UNIQUE (chain, id))`
	auditImportsDDL = `CREATE TABLE IF NOT EXISTS audit_chain_imports (
		chain TEXT PRIMARY KEY, source TEXT NOT NULL, rows INTEGER NOT NULL, at TEXT NOT NULL)`
	auditHeadDDL = `CREATE TABLE IF NOT EXISTS audit_chain_head (
		chain TEXT PRIMARY KEY, pos INTEGER NOT NULL, id TEXT NOT NULL, hash TEXT NOT NULL)`
	auditAnchorGet = `SELECT pos, id, hash FROM audit_chain_head WHERE chain = ?`
	auditAnchorSet = `INSERT INTO audit_chain_head (chain, pos, id, hash) VALUES (?, ?, ?, ?)
		ON CONFLICT (chain) DO UPDATE SET pos = excluded.pos, id = excluded.id, hash = excluded.hash`
	auditCols   = `id, created_at, acting_project_id, target_project_id, actor_id, action, target_table, target_id, override, before_hash, after_hash, reason, session_id, confirmed, prev_log_hash`
	auditHead   = `SELECT pos, ` + auditCols + ` FROM audit_chain WHERE chain = ? ORDER BY pos DESC LIMIT 1`
	auditAll    = `SELECT pos, ` + auditCols + ` FROM audit_chain WHERE chain = ? ORDER BY pos`
	auditInsert = `INSERT INTO audit_chain (chain, pos, ` + auditCols + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	auditMarked = `SELECT COUNT(*) FROM audit_chain_imports WHERE chain = ?`
	auditMark   = `INSERT INTO audit_chain_imports (chain, source, rows, at) VALUES (?, ?, ?, ?)`
)

// AuditChain is the chain table of one machine store file. Safe for
// concurrent use; processes share it through SQLite's lock (BEGIN
// IMMEDIATE + busy_timeout, fileDSN).
type AuditChain struct {
	db *sql.DB
}

// OpenAuditChain opens (creating it and its folder when missing) the
// chain tables in the machine store file at path. The caller closes it.
func OpenAuditChain(ctx context.Context, path string) (*AuditChain, error) {
	if err := os.MkdirAll(filepath.Dir(path), localDirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "audit chain: create "+filepath.Dir(path), err)
	}
	dsn, err := fileDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, dbErr("open audit chain", err)
	}
	db.SetMaxOpenConns(1)
	if err := execTx(ctx, db, auditDDL, auditImportsDDL, auditHeadDDL); err != nil {
		return nil, errors.Join(dbErr("create audit chain tables", err), db.Close())
	}
	return &AuditChain{db: db}, nil
}

// Close closes the handle.
func (a *AuditChain) Close() error { return a.db.Close() }

// Imported reports whether chain already took its one Import.
func (a *AuditChain) Imported(ctx context.Context, chain string) (bool, error) {
	var n int
	if err := a.db.QueryRowContext(ctx, auditMarked, chain).Scan(&n); err != nil {
		return false, dbErr("audit chain: read the import marker", err)
	}
	return n > 0, nil
}

// Import copies recs (oldest first, verbatim: ids, times and links kept, so
// the chain verifies as it did) to the end of chain, once: a second call
// for the same chain, from this or another process, copies nothing and
// returns 0. The marker is written even for no records, so the chain's
// first Append after it never races a late import. source names where the
// rows came from (a lore.db path), for doctor and support.
func (a *AuditChain) Import(ctx context.Context, chain, source string, recs []auditchain.Record, now time.Time) (n int, err error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, dbErr("audit chain: begin import", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	var marked int
	if err = tx.QueryRowContext(ctx, auditMarked, chain).Scan(&marked); err != nil {
		return 0, dbErr("audit chain: read the import marker", err)
	}
	if marked > 0 {
		return 0, tx.Commit()
	}
	pos, head, err := chainHead(ctx, tx, chain)
	if err != nil {
		return 0, err
	}
	anchored, err := headAnchored(ctx, tx, chain, pos, head)
	if err != nil {
		return 0, err
	}
	for _, r := range recs {
		pos++
		if err = insertRecord(ctx, tx, chain, pos, r); err != nil {
			return 0, err
		}
	}
	if anchored && len(recs) > 0 {
		if err = setAnchor(ctx, tx, chain, pos, recs[len(recs)-1]); err != nil {
			return 0, err
		}
	}
	if _, err = tx.ExecContext(ctx, auditMark, chain, source, len(recs), now.UTC().Format(time.RFC3339Nano)); err != nil {
		return 0, dbErr("audit chain: write the import marker", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, dbErr("audit chain: commit import", err)
	}
	return len(recs), nil
}

// Append adds e at the end of chain with a fresh aud_ id, created_at = now
// and the link to the head, in one immediate transaction, and returns the
// stored record. e's ID, CreatedAt and PrevLogHash are ignored.
func (a *AuditChain) Append(ctx context.Context, chain string, e auditchain.Record, now time.Time) (_ auditchain.Record, err error) {
	id, err := ids.New[auditTag]()
	if err != nil {
		return auditchain.Record{}, errs.Wrap(errs.Internal, "audit chain: new id", err)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return auditchain.Record{}, dbErr("audit chain: begin", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	pos, head, err := chainHead(ctx, tx, chain)
	if err != nil {
		return auditchain.Record{}, err
	}
	anchored, err := headAnchored(ctx, tx, chain, pos, head)
	if err != nil {
		return auditchain.Record{}, err
	}
	e.ID, e.CreatedAt, e.PrevLogHash = id.String(), now.UTC().Format(time.RFC3339), "" // v0.1.17 showed seconds
	if pos > 0 {
		e.PrevLogHash = auditchain.Hash(head)
	}
	if err = insertRecord(ctx, tx, chain, pos+1, e); err != nil {
		return auditchain.Record{}, err
	}
	if anchored {
		if err = setAnchor(ctx, tx, chain, pos+1, e); err != nil {
			return auditchain.Record{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return auditchain.Record{}, dbErr("audit chain: commit", err)
	}
	return e, nil
}

// Records returns chain oldest first.
func (a *AuditChain) Records(ctx context.Context, chain string) (_ []auditchain.Record, err error) {
	rows, err := a.db.QueryContext(ctx, auditAll, chain)
	if err != nil {
		return nil, dbErr("audit chain: read", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []auditchain.Record
	for rows.Next() {
		var pos int64
		r, serr := scanRecord(rows, &pos)
		if serr != nil {
			return nil, serr
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, dbErr("audit chain: read", err)
	}
	return out, nil
}

// Verify walks chain in insertion order (auditchain.Verify), then checks
// the newest entry against the head anchor (see the file comment): the id
// of the first entry found wrong ("" = intact; the anchored id when the
// newest entries were deleted) and the count verified.
func (a *AuditChain) Verify(ctx context.Context, chain string) (string, int, error) {
	recs, err := a.Records(ctx, chain)
	if err != nil {
		return "", 0, err
	}
	broken, n := auditchain.Verify(recs)
	if broken != "" {
		return broken, n, nil
	}
	anc, has, err := readAnchor(a.db.QueryRowContext(ctx, auditAnchorGet, chain))
	if err != nil {
		return "", 0, err
	}
	broken, n = checkAnchor(recs, anc, has)
	return broken, n, nil
}

// anchor is the audit_chain_head row of one chain.
type anchor struct {
	pos  int64
	id   string
	hash string
}

// checkAnchor compares the newest of recs (already link-verified) with the
// anchor: ("", len) when they agree or the chain and anchor are both empty.
func checkAnchor(recs []auditchain.Record, anc anchor, has bool) (string, int) {
	n := len(recs)
	switch {
	case n == 0 && !has:
		return "", 0
	case n == 0:
		return anc.id, 0 // every entry deleted
	case !has:
		return recs[n-1].ID, n - 1 // no anchor: the newest entry is unproven
	case int64(n) < anc.pos:
		return anc.id, n // the newest entries were deleted
	case int64(n) > anc.pos || recs[n-1].ID != anc.id || auditchain.Hash(recs[n-1]) != anc.hash:
		return recs[n-1].ID, n - 1
	}
	return "", n
}

// readAnchor scans one auditAnchorGet row (has false: no anchor).
func readAnchor(row *sql.Row) (anchor, bool, error) {
	var anc anchor
	err := row.Scan(&anc.pos, &anc.id, &anc.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return anchor{}, false, nil
	}
	if err != nil {
		return anchor{}, false, dbErr("audit chain: read the head anchor", err)
	}
	return anc, true, nil
}

// headAnchored reports whether the anchor may move with the next write:
// the chain is empty with no anchor, or its head (pos, record) is exactly
// the anchored one. Anything else is a tamper the anchor keeps showing.
func headAnchored(ctx context.Context, tx *sql.Tx, chain string, pos int64, head auditchain.Record) (bool, error) {
	anc, has, err := readAnchor(tx.QueryRowContext(ctx, auditAnchorGet, chain))
	if err != nil {
		return false, err
	}
	if !has {
		return pos == 0, nil
	}
	return pos > 0 && anc.pos == pos && anc.id == head.ID && anc.hash == auditchain.Hash(head), nil
}

// setAnchor records r at pos as chain's newest entry.
func setAnchor(ctx context.Context, tx *sql.Tx, chain string, pos int64, r auditchain.Record) error {
	if _, err := tx.ExecContext(ctx, auditAnchorSet, chain, pos, r.ID, auditchain.Hash(r)); err != nil {
		return dbErr("audit chain: move the head anchor", err)
	}
	return nil
}

// chainHead is the last position of chain and its record (0 when empty).
func chainHead(ctx context.Context, tx *sql.Tx, chain string) (pos int64, r auditchain.Record, err error) {
	rows, err := tx.QueryContext(ctx, auditHead, chain)
	if err != nil {
		return 0, r, dbErr("audit chain: read the head", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if rows.Next() {
		if r, err = scanRecord(rows, &pos); err != nil {
			return 0, r, err
		}
	}
	if err = rows.Err(); err != nil {
		return 0, r, dbErr("audit chain: read the head", err)
	}
	return pos, r, nil
}

// scanRecord reads one pos + auditCols row.
func scanRecord(s *sql.Rows, pos *int64) (auditchain.Record, error) {
	var r auditchain.Record
	var override, confirmed int
	if err := s.Scan(pos, &r.ID, &r.CreatedAt, &r.ActingProjectID, &r.TargetProjectID, &r.ActorID, &r.Action,
		&r.TargetTable, &r.TargetID, &override, &r.BeforeHash, &r.AfterHash, &r.Reason, &r.SessionID, &confirmed, &r.PrevLogHash); err != nil {
		return r, dbErr("audit chain: scan", err)
	}
	r.Override, r.Confirmed = override != 0, confirmed != 0
	return r, nil
}

// insertRecord writes r at pos of chain.
func insertRecord(ctx context.Context, tx *sql.Tx, chain string, pos int64, r auditchain.Record) error {
	if _, err := tx.ExecContext(ctx, auditInsert, chain, pos, r.ID, r.CreatedAt, r.ActingProjectID, r.TargetProjectID,
		r.ActorID, r.Action, r.TargetTable, r.TargetID, boolInt(r.Override), r.BeforeHash, r.AfterHash, r.Reason,
		r.SessionID, boolInt(r.Confirmed), r.PrevLogHash); err != nil {
		return dbErr("audit chain: insert", err)
	}
	return nil
}

// boolInt is SQLite's 0/1 for b.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The v0.1.17 lore.db audit_logs reader (the one import read, P3-LOCAL-7).
const (
	legacyAuditHasTable = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'audit_logs'`
	legacyAuditHasSes   = `SELECT COUNT(*) FROM pragma_table_info('audit_logs') WHERE name IN ('session_id', 'confirmed')`
	legacyAuditCols     = `id, created_at, COALESCE(acting_project_id, ''), COALESCE(target_project_id, ''), actor_id, action,
		COALESCE(target_table, ''), COALESCE(target_id, ''), override, COALESCE(before_hash, ''), COALESCE(after_hash, ''),
		COALESCE(reason, ''), COALESCE(prev_log_hash, '')`
	legacyAuditAll    = `SELECT ` + legacyAuditCols + `, '', 0 FROM audit_logs ORDER BY rowid`
	legacyAuditAllSes = `SELECT ` + legacyAuditCols + `, COALESCE(session_id, ''), confirmed FROM audit_logs ORDER BY rowid`
)

// ReadLegacyAuditLog reads the v0.1.17 hash chain of the lore.db at path,
// oldest first, read-only, as saas/pkg/aicoder/audit stored it (a lore.db
// without the 0.1.18 session columns reads them as empty, as v0.1.17's
// reader did). A file without the table reads as no records. created_at is
// RFC 3339 UTC seconds, the form `lore audit log` always printed.
func ReadLegacyAuditLog(ctx context.Context, path string) (_ []auditchain.Record, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errs.Wrap(errs.BadPath, "audit chain: lore.db path", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro&_pragma=busy_timeout(" + strconv.Itoa(busyTimeoutMS) + ")"}
	db, err := sql.Open(driverName, u.String())
	if err != nil {
		return nil, dbErr("open lore.db for the audit import", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var n int
	if err = db.QueryRowContext(ctx, legacyAuditHasTable).Scan(&n); err != nil {
		return nil, dbErr("lore.db: read the tables", err)
	}
	if n == 0 {
		return nil, nil
	}
	if err = db.QueryRowContext(ctx, legacyAuditHasSes).Scan(&n); err != nil {
		return nil, dbErr("lore.db: read the audit columns", err)
	}
	q := legacyAuditAll
	if n == 2 {
		q = legacyAuditAllSes
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, dbErr("lore.db: read audit_logs", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []auditchain.Record
	for rows.Next() {
		var r auditchain.Record
		var created sql.NullTime
		if err = rows.Scan(&r.ID, &created, &r.ActingProjectID, &r.TargetProjectID, &r.ActorID, &r.Action, &r.TargetTable,
			&r.TargetID, &r.Override, &r.BeforeHash, &r.AfterHash, &r.Reason, &r.PrevLogHash, &r.SessionID, &r.Confirmed); err != nil {
			return nil, dbErr("lore.db: scan audit_logs", err)
		}
		if created.Valid {
			r.CreatedAt = created.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, dbErr("lore.db: read audit_logs", err)
	}
	return out, nil
}
