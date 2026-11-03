package lsync

// live.go — the engine half of live rows (LORE_TEAM_SPEC.md §3, F1, F2).
//
// lsync knows nothing about git refs. It offers two seams:
//
//   - Options.Divert: during a pass, a row the caller routes elsewhere (the
//     live ref) is NOT exported to .lore/data. Its canonical document is
//     returned in Report.Diverted, and it stays dirty until the caller has
//     stored it and calls ClearDirty: a crash in between re-diverts it on the
//     next pass, never loses it. File-side changes of a diverted row are
//     recorded as the new base but not imported, because live wins over the
//     branch copy (fixed rule 4).
//
//   - LiveApply: brings the caller's view of the live folder into lore.db
//     (live wins), remembering per row the content it applied in
//     _lore_live_rows, so an unchanged live row is never re-imported and the
//     router knows which rows are live.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// tblLive records, per row, the live content last applied to lore.db.
//
// Field notes: hash is DocHash-independent — it is the sha256 of the
// canonical body bytes the caller passed (so it matches exactly what is on
// the live ref); deleted=1 marks a tombstone (the row is absent from the DB
// on purpose).
const tblLive = "_lore_live_rows"

// liveSchema creates tblLive; run by EnsureInfra's callers through
// ensureLiveSchema (kept apart so the bookkeeping list above stays the
// branch-sync contract of LORE_SYNC_SPEC.md).
const liveSchema = `CREATE TABLE IF NOT EXISTS ` + tblLive + ` (
	table_name TEXT NOT NULL,
	row_id TEXT NOT NULL,
	hash TEXT NOT NULL,
	deleted INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (table_name, row_id))`

func ensureLiveSchema(ctx context.Context, q execer) error {
	if _, err := q.ExecContext(ctx, liveSchema); err != nil {
		return fmt.Errorf("lsync: ensure live schema: %w", err)
	}
	return nil
}

// RowRef names a row for Divert and ClearDirty.
type RowRef struct {
	Table string
	ID    string
	// HasBase: the row has a file in this branch's .lore/data (a base).
	HasBase bool
	// Deleted: the DB no longer holds the row.
	Deleted bool
}

// DivertedRow is a dirty row the pass handed back instead of exporting it.
type DivertedRow struct {
	RowRef
	// Doc is the canonical row document (what a .lore/data file would
	// hold); nil when the row was deleted.
	Doc []byte
}

// divert handles one row under Options.Divert. handled=false means the
// normal export path applies.
func (e *engine) divert(ctx context.Context, k rowKey, t *Table, fc fileChange, fileChanged, dirty bool) (bool, error) {
	rel := k.rel()
	_, hasBase := e.bases[rel]
	dbDoc, err := readRowDoc(ctx, e.tx, t, liveColumns(t, e.present), k.id)
	if err != nil {
		return false, err
	}
	ref := RowRef{Table: k.table, ID: k.id, HasBase: hasBase || (fileChanged && !fc.deleted), Deleted: dbDoc == nil}
	if !e.o.Divert(ref) {
		return false, nil
	}
	if fileChanged {
		// Live wins: the branch file's new content becomes the base (so it is
		// not re-examined every pass) but is never imported.
		if fc.deleted {
			if err := e.dropBase(ctx, rel); err != nil {
				return true, err
			}
		} else if err := e.recordBase(ctx, k, fc.bytes, fc.stat); err != nil {
			return true, err
		}
	}
	if !dirty {
		return true, nil
	}
	d := DivertedRow{RowRef: ref}
	if dbDoc != nil {
		extra, err := e.loadExtra(ctx, rel)
		if err != nil {
			return true, err
		}
		if d.Doc, err = encodeDoc(dbDoc, extra); err != nil {
			return true, err
		}
		if e.o.CheckSecrets != nil {
			if why := e.o.CheckSecrets(d.Doc); why != "" {
				e.recordError(ctx, rel, ErrKindSecret, why+" — not written to the live ref; edit the row or set LORE_SYNC_ALLOW_SECRETS=1 for one run")
				e.deferred[k] = true
				return true, nil
			}
		}
	}
	// Deleted, or passed the secret check: an earlier secret refusal of this
	// row no longer holds (BUG-OPS1: doctor kept reporting it).
	e.clearErrorKind(ctx, rel, ErrKindSecret)
	if !e.deferred[k] {
		e.rep.Diverted = append(e.rep.Diverted, d)
	}
	// Stays dirty until the caller stored it (ClearDirty); deferred keeps
	// later drain rounds of this pass from diverting it twice.
	e.deferred[k] = true
	return true, nil
}

// recordBase stores new base bytes for rel WITHOUT clearing the row's dirty
// flag (setBase clears it; a diverted row must stay dirty until stored).
func (e *engine) recordBase(ctx context.Context, k rowKey, b []byte, st fileStat) error {
	rel := k.rel()
	nowNs := e.now.UnixNano()
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+tblFiles+`(path, table_name, row_id, hash, base, size, mtime_ns, synced_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET hash = excluded.hash, base = excluded.base, size = excluded.size,
			mtime_ns = excluded.mtime_ns, synced_at_ns = excluded.synced_at_ns`,
		rel, k.table, k.id, hashBytes(b), b, st.size, st.mtimeNs, nowNs); err != nil {
		return fmt.Errorf("lsync: record base %s: %w", rel, err)
	}
	e.bases[rel] = baseEntry{hash: hashBytes(b), base: b, size: st.size, mtimeNs: st.mtimeNs, syncedAtNs: nowNs}
	return nil
}

// DirtyRefs lists the rows lore.db marked changed since their last sync,
// with whether each has a branch file (base) and whether it was deleted, so
// a caller can decide every row of a command at once (rowroute.DecideAll)
// before a pass diverts them.
func DirtyRefs(ctx context.Context, db *sql.DB) ([]RowRef, error) {
	reg, _ := NewRegistry()
	if err := EnsureInfra(ctx, db, reg); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT d.table_name, d.row_id, f.path IS NOT NULL
		FROM `+tblDirty+` d LEFT JOIN `+tblFiles+` f ON f.table_name = d.table_name AND f.row_id = d.row_id
		ORDER BY d.table_name, d.row_id`)
	if err != nil {
		return nil, fmt.Errorf("lsync: list dirty: %w", err)
	}
	var out []RowRef
	for rows.Next() {
		var r RowRef
		if err := rows.Scan(&r.Table, &r.ID, &r.HasBase); err != nil {
			_ = rows.Close() // the scan error is the one to report
			return nil, fmt.Errorf("lsync: scan dirty: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		t, ok := reg.Table(out[i].Table)
		if !ok {
			continue
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(t.Name)+` WHERE id = ?`, out[i].ID).Scan(&n); err != nil {
			return nil, fmt.Errorf("lsync: probe %s: %w", t.Name, err)
		}
		out[i].Deleted = n == 0
	}
	return out, nil
}

// ClearDirty marks rows as stored (after the caller wrote diverted rows to
// their destination).
func ClearDirty(ctx context.Context, db *sql.DB, rows []RowRef) error {
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `DELETE FROM `+tblDirty+` WHERE table_name = ? AND row_id = ?`, r.Table, r.ID); err != nil {
			return fmt.Errorf("lsync: clear dirty: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- LiveApply

// LiveKey names one row of the live folder.
type LiveKey struct{ Table, ID string }

// LiveDoc is the live version of one row.
type LiveDoc struct {
	// Body is the canonical row document without the `_live` block (nil
	// for a tombstone).
	Body    []byte
	Deleted bool
}

// LiveState is what lore.db last applied for a row.
type LiveState struct {
	Hash    string
	Deleted bool
}

// LiveApplyReport lists what LiveApply changed.
type LiveApplyReport struct {
	Imported []LiveKey
	Deleted  []LiveKey
	// Left are rows no longer on the live ref (landed and confirmed); their
	// DB copy is kept (it is main's content) unless they were purged.
	Left   []LiveKey
	Errors []FileError
	// Conflicted are rows imported with conflict markers in a text field.
	Conflicted []LiveKey
}

// Changed reports whether lore.db changed.
func (r LiveApplyReport) Changed() bool {
	return len(r.Imported)+len(r.Deleted) > 0
}

// LiveApplyOptions configures LiveApply.
type LiveApplyOptions struct {
	DB *sql.DB
	// Rows is the complete live folder of this project.
	Rows map[LiveKey]LiveDoc
	// Unreadable lists rows still on live whose file could not be read.
	// They are not in Rows, but they have not left live either: the
	// leave-live pass skips them, so a damaged file never drops a row's
	// live bookkeeping or replaces it with main's copy.
	Unreadable map[LiveKey]bool
	// Purged ids leave lore.db when they leave live (`lore live purge`).
	Purged map[string]bool
	// Now stamps bookkeeping; zero = time.Now.
	Now time.Time
	// Landed returns the copy a row that left live has on the fetched
	// default branch (ok=false when main has none). A row leaves live only
	// once main holds exactly its final live content (F5), so that copy is
	// the row's current version; a clone that last saw an older live
	// version must not keep showing it. nil keeps the DB copy as is.
	Landed func(LiveKey) ([]byte, bool)
}

// LiveRowHash is the hash LiveApply records for a live version, exported
// so the caller can record its own writes (RecordLive) with the same value.
func LiveRowHash(d LiveDoc) string {
	if d.Deleted {
		return "deleted"
	}
	return hashBytes(d.Body)
}

// LiveApply makes lore.db hold the live version of every live row (F1:
// live wins). Rows whose live content is what was applied last time are
// skipped; tombstones delete the DB row (the row is kept in the sync trash);
// rows that left live keep their DB copy unless purged.
func LiveApply(ctx context.Context, o LiveApplyOptions) (LiveApplyReport, error) {
	var rep LiveApplyReport
	reg, _ := NewRegistry()
	if err := ensureLiveSchema(ctx, o.DB); err != nil {
		return rep, err
	}
	present, err := existingColumns(ctx, o.DB)
	if err != nil {
		return rep, err
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	e := &engine{o: Options{DB: o.DB}, reg: reg, present: present, now: now.UTC(), rep: &Report{},
		blocked: map[string]bool{}, unsettled: map[string]bool{}, deferred: map[rowKey]bool{}}
	err = e.inTx(ctx, func(ctx context.Context) error {
		recorded, err := loadLiveStates(ctx, e.tx)
		if err != nil {
			return err
		}
		for _, k := range sortedLiveKeys(o.Rows) {
			if err := e.applyLive(ctx, k, o.Rows[k], recorded, &rep); err != nil {
				return err
			}
		}
		for k, st := range recorded {
			if _, still := o.Rows[k]; still || o.Unreadable[k] {
				continue
			}
			if err := e.leaveLive(ctx, k, st, o.Purged[k.ID], &rep); err != nil {
				return err
			}
			if o.Landed == nil || o.Purged[k.ID] || st.Deleted {
				continue
			}
			if b, ok := o.Landed(k); ok {
				if err := e.importLanded(ctx, k, b, &rep); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return rep, err
}

func (e *engine) applyLive(ctx context.Context, k LiveKey, d LiveDoc, recorded map[LiveKey]LiveState, rep *LiveApplyReport) error {
	t, ok := e.reg.Table(k.Table)
	if !ok {
		return nil // a table this binary does not sync: leave it alone
	}
	h := LiveRowHash(d)
	if st, seen := recorded[k]; seen && st.Hash == h {
		return nil
	}
	rk := rowKey{k.Table, k.ID}
	if d.Deleted {
		if err := e.deleteRowToTrash(ctx, rk, t, "deleted in live"); err != nil {
			return err
		}
		rep.Deleted = append(rep.Deleted, k)
		return recordLive(ctx, e.tx, k, LiveState{Hash: h, Deleted: true})
	}
	doc, err := decodeDoc(d.Body)
	if err != nil {
		rep.Errors = append(rep.Errors, FileError{Path: "live:" + rk.rel(), Kind: ErrKindInvalid, Detail: err.Error()})
		return nil
	}
	if kind, detail := validateRowFile(d.Body, k.Table, k.ID); kind != "" && kind != ErrKindConflict {
		rep.Errors = append(rep.Errors, FileError{Path: "live:" + rk.rel(), Kind: kind, Detail: detail})
		return nil
	} else if kind == ErrKindConflict {
		rep.Conflicted = append(rep.Conflicted, k)
		if err := e.saveConflictCopy(ctx, rk, KeptLive, doc); err != nil {
			return err
		}
	}
	if err := e.importDoc(ctx, rk, t, doc); err != nil {
		rep.Errors = append(rep.Errors, FileError{Path: "live:" + rk.rel(), Kind: ErrKindImport, Detail: err.Error()})
		return nil
	}
	if err := e.clearDirty(ctx, rk); err != nil {
		return err
	}
	rep.Imported = append(rep.Imported, k)
	return recordLive(ctx, e.tx, k, LiveState{Hash: h})
}

// leaveLive forgets a row that is no longer on the live ref. Its DB copy is
// what landed on main, so it stays — except a purged id, which must vanish
// from every clone (§6.6).
func (e *engine) leaveLive(ctx context.Context, k LiveKey, st LiveState, purged bool, rep *LiveApplyReport) error {
	if purged && !st.Deleted {
		if t, ok := e.reg.Table(k.Table); ok {
			if err := e.deleteRowToTrash(ctx, rowKey{k.Table, k.ID}, t, "purged from live"); err != nil {
				return err
			}
			rep.Deleted = append(rep.Deleted, k)
		}
	}
	if _, err := e.tx.ExecContext(ctx, `DELETE FROM `+tblLive+` WHERE table_name = ? AND row_id = ?`, k.Table, k.ID); err != nil {
		return fmt.Errorf("lsync: forget live row: %w", err)
	}
	rep.Left = append(rep.Left, k)
	return nil
}

// importLanded brings a row that left live to its landed copy (one that
// does not decode or validate is skipped, never imported half).
func (e *engine) importLanded(ctx context.Context, k LiveKey, b []byte, rep *LiveApplyReport) error {
	t, ok := e.reg.Table(k.Table)
	if !ok {
		return nil
	}
	if kind, _ := validateRowFile(b, k.Table, k.ID); kind != "" {
		return nil
	}
	doc, err := decodeDoc(b)
	if err != nil {
		return nil
	}
	rk := rowKey{k.Table, k.ID}
	if err := e.importDoc(ctx, rk, t, doc); err != nil {
		rep.Errors = append(rep.Errors, FileError{Path: "main:" + rk.rel(), Kind: ErrKindImport, Detail: err.Error()})
		return nil
	}
	if err := e.clearDirty(ctx, rk); err != nil {
		return err
	}
	rep.Imported = append(rep.Imported, k)
	return nil
}

// KeptLive marks a conflict record made for a live row with markers (the
// live version is what lore shows; the record makes `lore sync conflicts`
// list it until it is settled).
const KeptLive KeptSide = "live"

func recordLive(ctx context.Context, q execer, k LiveKey, st LiveState) error {
	del := 0
	if st.Deleted {
		del = 1
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO `+tblLive+`(table_name, row_id, hash, deleted) VALUES (?, ?, ?, ?)
		ON CONFLICT(table_name, row_id) DO UPDATE SET hash = excluded.hash, deleted = excluded.deleted`,
		k.Table, k.ID, st.Hash, del); err != nil {
		return fmt.Errorf("lsync: record live row: %w", err)
	}
	return nil
}

// RecordLive records that lore.db holds the given live version (the
// caller just wrote this DB row to the live ref), so the next LiveApply does
// not re-import it.
func RecordLive(ctx context.Context, db *sql.DB, k LiveKey, d LiveDoc) error {
	if err := ensureLiveSchema(ctx, db); err != nil {
		return err
	}
	return recordLive(ctx, db, k, LiveState{Hash: LiveRowHash(d), Deleted: d.Deleted})
}

// LiveStates returns every row lore.db currently holds as live.
func LiveStates(ctx context.Context, db *sql.DB) (map[LiveKey]LiveState, error) {
	if err := ensureLiveSchema(ctx, db); err != nil {
		return nil, err
	}
	return loadLiveStates(ctx, db)
}

func loadLiveStates(ctx context.Context, q execer) (map[LiveKey]LiveState, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name, row_id, hash, deleted FROM `+tblLive)
	if err != nil {
		return nil, fmt.Errorf("lsync: load live rows: %w", err)
	}
	defer rows.Close()
	out := map[LiveKey]LiveState{}
	for rows.Next() {
		var k LiveKey
		var st LiveState
		var del int
		if err := rows.Scan(&k.Table, &k.ID, &st.Hash, &del); err != nil {
			return nil, fmt.Errorf("lsync: scan live row: %w", err)
		}
		st.Deleted = del != 0
		out[k] = st
	}
	return out, rows.Err()
}

func sortedLiveKeys(m map[LiveKey]LiveDoc) []LiveKey {
	out := make([]LiveKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RowDoc reads one row's canonical document from lore.db (what an export
// would write), ok=false when absent.
func RowDoc(ctx context.Context, db *sql.DB, table, id string) ([]byte, bool, error) {
	reg, _ := NewRegistry()
	t, ok := reg.Table(table)
	if !ok {
		return nil, false, fmt.Errorf("lsync: unknown table %q", table)
	}
	present, err := existingColumns(ctx, db)
	if err != nil {
		return nil, false, err
	}
	doc, err := readRowDoc(ctx, db, t, liveColumns(t, present), id)
	if err != nil || doc == nil {
		return nil, false, err
	}
	b, err := encodeDoc(doc, nil)
	return b, err == nil, err
}

// TableSynced reports whether a table name is a synced table.
func TableSynced(name string) bool {
	reg, _ := NewRegistry()
	_, ok := reg.Table(name)
	return ok
}

// AddPurged returns _purged.json bytes holding existing's ids (nil =
// absent) plus ids, in the canonical format the merge driver unions. A
// landed tombstone records its id here (F5) so an old branch copy can never
// be imported again.
func AddPurged(existing []byte, ids []string) ([]byte, error) {
	set := map[string]bool{}
	if len(existing) > 0 {
		doc, err := decodeDoc(existing)
		if err != nil {
			return nil, fmt.Errorf("lsync: %s: %w", PurgedFileName, err)
		}
		ids, err := purgedIDList(doc)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			set[id] = true
		}
	}
	for _, id := range ids {
		set[id] = true
	}
	return encodePurged(set)
}

// PurgedIDs decodes _purged.json bytes into its ids. A file that does not
// decode, or whose "ids" value is not a list, is an error: callers decide
// what to import or land on this set, and an empty set would let purged
// rows back.
func PurgedIDs(b []byte) ([]string, error) {
	doc, err := decodeDoc(b)
	if err != nil {
		return nil, fmt.Errorf("lsync: %s: %w", PurgedFileName, err)
	}
	return purgedIDList(doc)
}

// purgedIDList reads the id list of a decoded _purged.json. A missing list is
// empty; an "ids" value that is not a list is an error, so rewriting the file
// can never quietly drop ids that were purged before (they would come back).
// Non-string entries are skipped: they name no row, so skipping them
// resurrects nothing (the same reading as readPurged and the merge driver).
func purgedIDList(doc map[string]any) ([]string, error) {
	raw, has := doc[purgedKeyIDs]
	if !has {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("lsync: %s: %q is %T, want a list of ids", PurgedFileName, purgedKeyIDs, raw)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if id, ok := v.(string); ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// ImportMissing imports row documents whose id lore.db does not hold yet,
// and nothing else: rows that landed on the fetched default branch after
// this checkout's branch point, read by a person standing on the default
// branch who has not pulled (F1: main is their durable home, and no other
// copy competes). Rows already in lore.db, invalid documents and documents
// with conflict markers are skipped. Returns the imported keys.
func ImportMissing(ctx context.Context, db *sql.DB, docs [][]byte) ([]LiveKey, error) {
	reg, _ := NewRegistry()
	present, err := existingColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	e := &engine{o: Options{DB: db}, reg: reg, present: present, now: time.Now().UTC(), rep: &Report{},
		blocked: map[string]bool{}, unsettled: map[string]bool{}, deferred: map[rowKey]bool{}}
	var out []LiveKey
	err = e.inTx(ctx, func(ctx context.Context) error {
		for _, b := range docs {
			doc, err := decodeDoc(b)
			if err != nil {
				continue
			}
			table, _ := doc[keyTable].(string)
			id, _ := doc[idColumn].(string)
			t, ok := reg.Table(table)
			if !ok || id == "" {
				continue
			}
			if kind, _ := validateRowFile(b, table, id); kind != "" {
				continue
			}
			cur, err := readRowDoc(ctx, e.tx, t, liveColumns(t, e.present), id)
			if err != nil {
				return err
			}
			if cur != nil {
				continue
			}
			if err := e.importDoc(ctx, rowKey{table, id}, t, doc); err != nil {
				continue // a row the DB refuses stays on main; nothing to repair here
			}
			out = append(out, LiveKey{Table: table, ID: id})
		}
		return nil
	})
	return out, err
}

// DropRows removes rows that were never shared (a live write the leak
// check refused, F16) from lore.db: each goes to the sync trash with
// reason (`lore sync trash restore` brings it back) and leaves the dirty
// set, so nothing is retried or pushed later.
func DropRows(ctx context.Context, db *sql.DB, rows []RowRef, reason string) error {
	if len(rows) == 0 {
		return nil
	}
	reg, _ := NewRegistry()
	present, err := existingColumns(ctx, db)
	if err != nil {
		return err
	}
	e := &engine{o: Options{DB: db}, reg: reg, present: present, now: time.Now().UTC(), rep: &Report{},
		blocked: map[string]bool{}, unsettled: map[string]bool{}, deferred: map[rowKey]bool{}}
	return e.inTx(ctx, func(ctx context.Context) error {
		for _, r := range rows {
			t, ok := reg.Table(r.Table)
			if !ok {
				continue
			}
			if err := e.deleteRowToTrash(ctx, rowKey{r.Table, r.ID}, t, reason); err != nil {
				return err
			}
		}
		return nil
	})
}
