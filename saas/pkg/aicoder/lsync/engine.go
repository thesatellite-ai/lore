package lsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"
	"saas/pkg/aicoder/processlock"

	"entgo.io/ent/schema/field"
)

// PreferMode decides ADOPT clashes (§12.3, E43).
type PreferMode string

const (
	// PreferNewest: the side with the later updated_at wins; the other is
	// kept as a conflict copy. Default.
	PreferNewest PreferMode = ""
	// PreferDB: the DB wins every clash (used after `lore restore`, where
	// bringing the DB back is the user's intent).
	PreferDB PreferMode = "db"
	// PreferFiles: the files win every clash.
	PreferFiles PreferMode = "files"
)

// PreferModes is the canonical list of PreferMode values for flag parsing.
var PreferModes = []PreferMode{PreferNewest, PreferDB, PreferFiles}

// PassKind says which reconcile algorithm ran.
type PassKind string

const (
	// PassNormal: steady state — files and dirty rows reconciled (§8.2).
	PassNormal PassKind = "normal"
	// PassBootstrap: first pass with no .lore/data yet; everything exported.
	PassBootstrap PassKind = "bootstrap"
	// PassAdopt: first pass of a DB in a checkout that already has
	// .lore/data (fresh clone, teammate's pull, restore).
	PassAdopt PassKind = "adopt"
	// PassSkipped: nothing ran (read-only before bootstrap, or the data
	// directory is missing after a baseline).
	PassSkipped PassKind = "skipped"
)

// Error kinds recorded in _lore_sync_errors and reported to the user.
const (
	// ErrKindInvalid: not parseable, wrong envelope, or wrong path for its id.
	ErrKindInvalid = "invalid-file"
	// ErrKindSecret: a credential pattern matched; the row stays local.
	ErrKindSecret = "secret-detected"
	// ErrKindConflict: unresolved merge markers (raw or inside a value).
	ErrKindConflict = "conflict-markers"
	// ErrKindNewer: written by a newer lore format; upgrade to import it.
	ErrKindNewer = "newer-format"
	// ErrKindImport: valid file the DB refused (constraint, type mismatch).
	ErrKindImport = "import-failed"
)

// defaultLockTimeout bounds how long a command waits for another lore
// process's sync pass before giving up.
// ds:def id=sync-locktimeout-rh9uf4cq owner=@khanakia stability=stable desc="sync lock wait"
const defaultLockTimeout = 15 * time.Second

// lockPollInterval is the retry cadence while waiting for the sync lock.
const lockPollInterval = 50 * time.Millisecond

// maxDrainRounds bounds the drain loop. Natural-key merges and project
// adoption rewrite references, which marks MORE rows dirty; a second round
// exports them. More than a few rounds means something keeps re-dirtying
// rows, so stop instead of spinning.
// ds:def id=sync-drainrounds-2adqnp3a owner=@khanakia stability=stable desc="drain rounds per pass"
const maxDrainRounds = 4

// Options configures Reconcile.
type Options struct {
	// DB is the open lore.db.
	DB *sql.DB
	// DataDir is the absolute path of .lore/data.
	DataDir string
	// LockPath is the advisory lock file serialising sync passes across
	// processes (e.g. .lore/state/sync.lock). Empty disables locking (tests).
	LockPath string
	// ReadOnly imports file changes into the cache but never writes files,
	// never bootstraps, and leaves DB-side changes pending.
	ReadOnly bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// CheckSecrets returns a non-empty reason when a row file must not be
	// written (a credential pattern in the content). nil disables scanning.
	CheckSecrets func(content []byte) string
	// Backup snapshots lore.db before BOOTSTRAP / ADOPT and returns its
	// path. nil skips the snapshot (tests).
	Backup func(ctx context.Context) (string, error)
	// LockTimeout overrides defaultLockTimeout.
	LockTimeout time.Duration
	// DBChangeKind labels the DB-side changes this pass exports (see
	// OnChange): ChangeLocal for the end-of-command pass (the command's own
	// writes), ChangeExternal for the start-of-command pass (anything that
	// wrote to lore.db since the last lore command — another tool, sqlite3).
	// Empty means ChangeLocal.
	DBChangeKind ChangeKind
	// OnChange, when set, is called inside the pass's transaction for every
	// row whose DB state this pass changed or exported, so an audit trail can
	// be written atomically with the sync (q writes in the same transaction).
	// Returning an error aborts the pass.
	OnChange func(ctx context.Context, q Execer, c Change) error
	// DirtyOnly skips the full directory walk and examines only rows the
	// DB marked dirty (plus their own files). The CLI's end-of-command pass
	// uses it: the start-of-command pass already scanned everything, so
	// the second walk would only double the per-command cost. File changes
	// made by others meanwhile are picked up by the next full pass.
	DirtyOnly bool
}

// FileError is one file that could not be synced.
type FileError struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// KeptSide names which version a sync conflict kept (ConflictNote.Kept and
// the conflicts table's kept column).
type KeptSide string

const (
	// KeptFile: the .lore/data file's version won; the DB version was saved.
	KeptFile KeptSide = "file"
	// KeptDB: lore.db's version won and was exported; the file version was saved.
	KeptDB KeptSide = "db"
)

// ConflictNote is one row where both sides changed and a side was chosen.
type ConflictNote struct {
	Path string   `json:"path"`
	Kept KeptSide `json:"kept"`
	Why  string   `json:"why"`
}

// Report summarises one Reconcile call. Path lists are relative to DataDir.
type Report struct {
	Pass       PassKind       `json:"pass"`
	Imported   []string       `json:"imported,omitempty"`
	Exported   []string       `json:"exported,omitempty"`
	Removed    []string       `json:"removed_files,omitempty"`
	Deleted    []string       `json:"deleted_rows,omitempty"`
	Conflicts  []ConflictNote `json:"conflicts,omitempty"`
	Errors     []FileError    `json:"errors,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
	Merged     []string       `json:"natural_key_merges,omitempty"`
	BackupPath string         `json:"backup_path,omitempty"`
	// PendingExports counts DB changes left unexported (read-only mode,
	// secret refusals); they are retried on the next pass.
	PendingExports int `json:"pending_exports,omitempty"`
}

// Changed reports whether the pass changed anything on either side.
func (r Report) Changed() bool {
	return len(r.Imported)+len(r.Exported)+len(r.Removed)+len(r.Deleted)+len(r.Merged) > 0
}

// rowKey identifies one synced row.
type rowKey struct{ table, id string }

func (k rowKey) rel() string { return relPath(k.table, k.id) }

// baseEntry is a _lore_sync_files row.
type baseEntry struct {
	hash       string
	base       []byte
	size       int64
	mtimeNs    int64
	syncedAtNs int64
}

// engine carries the state of one pass.
type engine struct {
	o       Options
	reg     *Registry
	present map[string]map[string]bool
	tx      *sql.Tx
	now     time.Time
	rep     *Report
	scan    scanResult
	bases   map[string]baseEntry
	// blocked holds files that failed to parse this pass; they are never
	// overwritten by an export (see exportDoc).
	blocked map[string]bool
	// unsettled holds files imported this pass although a text field still
	// carries the merge driver's conflict markers. Their notice must outlive
	// the import (applyFile would otherwise clear it); exporting a settled
	// edit clears it.
	unsettled map[string]bool
	// idMerges are the natural-key merges done this pass (loser → winner).
	// Rows are processed in table order, so a file imported AFTER a merge
	// may still name the losing id; drain re-applies every rewrite after
	// each round so no reference to a merged-away id survives the pass.
	idMerges [][2]string
	// deferred holds rows whose export was skipped this pass (read-only,
	// blocked file, secret refusal). They stay dirty for the NEXT pass but
	// later drain rounds of this pass skip them, so each is reported once.
	deferred map[rowKey]bool
}

// Reconcile brings lore.db and .lore/data into agreement (see the package
// doc). It is safe to call at the start AND end of every command: a pass
// with nothing to do costs one directory walk plus two small queries.
//
// Invariants:
//   - Never deletes DB rows because the data directory is missing (an old
//     branch without lore data, a `git clean`): it warns and does nothing.
//   - Every row deleted from the DB because its file disappeared is copied
//     to the trash table first (`lore sync trash`).
//   - A file that cannot be parsed is never imported and never overwritten;
//     it is reported on every pass until fixed.
func Reconcile(ctx context.Context, o Options) (Report, error) {
	rep := Report{Pass: PassSkipped}
	if o.DB == nil || o.DataDir == "" {
		return rep, fmt.Errorf("lsync: Reconcile needs DB and DataDir")
	}
	unlock, err := acquireLock(ctx, o)
	if err != nil {
		return rep, err
	}
	defer unlock()

	reg, unclassified := NewRegistry()
	for _, t := range unclassified {
		rep.Warnings = append(rep.Warnings, "table "+t+" is not classified for sync; treated as local")
	}
	if err := EnsureInfra(ctx, o.DB, reg); err != nil {
		return rep, err
	}
	present, err := existingColumns(ctx, o.DB)
	if err != nil {
		return rep, err
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	e := &engine{o: o, reg: reg, present: present, now: now().UTC(), rep: &rep, blocked: map[string]bool{}, unsettled: map[string]bool{}, deferred: map[rowKey]bool{}}

	baselined, _, err := getMeta(ctx, o.DB, metaBaselined)
	if err != nil {
		return rep, err
	}
	dataExists := MetaExists(o.DataDir)
	switch {
	case baselined != metaFlagSet && !dataExists:
		if o.ReadOnly {
			rep.Warnings = append(rep.Warnings, "read-only: lore data not bootstrapped yet")
			return rep, nil
		}
		rep.Pass = PassBootstrap
		err = e.inTx(ctx, e.bootstrap)
	case baselined != metaFlagSet && dataExists:
		rep.Pass = PassAdopt
		err = e.inTx(ctx, e.adopt)
	case !dataExists:
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"%s is missing (a branch without lore data, or deleted): lore.db is left untouched; run `lore sync export --all` to recreate it", o.DataDir))
		return rep, nil
	default:
		rep.Pass = PassNormal
		err = e.inTx(ctx, e.normal)
	}
	if err != nil {
		return rep, err
	}
	if len(rep.Imported)+len(rep.Deleted) > 0 {
		if err := setMeta(ctx, o.DB, metaImportedAt, e.now.Format(time.RFC3339Nano)); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// acquireLock takes the cross-process sync lock, waiting up to the timeout.
// Read-only mode (LORE_READ_ONLY=1) makes processlock refuse; a read-only
// pass only refreshes the cache, so it proceeds unlocked.
func acquireLock(ctx context.Context, o Options) (func(), error) {
	if o.LockPath == "" {
		return func() {}, nil
	}
	timeout := o.LockTimeout
	if timeout == 0 {
		timeout = defaultLockTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		l, err := processlock.Acquire(o.LockPath)
		if err == nil {
			return func() { _ = l.Release() }, nil // release errors are not actionable here
		}
		if errors.Is(err, processlock.ErrReadOnly) {
			return func() {}, nil
		}
		if !errors.Is(err, processlock.ErrLockHeld) {
			return nil, fmt.Errorf("lsync: lock: %w", err)
		}
		poll := time.NewTimer(lockPollInterval)
		select {
		case <-ctx.Done():
			poll.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			poll.Stop()
			return nil, fmt.Errorf("lsync: another lore process held the sync lock for %s", timeout)
		case <-poll.C:
		}
	}
}

// inTx runs fn inside one DB transaction. File writes inside fn are not
// transactional; the ordering rules make that safe: a file written before a
// rollback is simply seen as a file-side change on the next pass.
func (e *engine) inTx(ctx context.Context, fn func(context.Context) error) error {
	tx, err := e.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lsync: begin: %w", err)
	}
	e.tx = tx
	defer func() { e.tx = nil }()
	if err := fn(ctx); err != nil {
		_ = tx.Rollback() // the fn error is the one worth reporting
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lsync: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- normal

// normal is the steady-state pass: compare files against the stat/base
// index, compare dirty DB rows against their base, and apply §12.2.
func (e *engine) normal(ctx context.Context) error {
	if e.o.DirtyOnly {
		return e.dirtyOnly(ctx)
	}
	if err := e.loadScanAndBases(ctx); err != nil {
		return err
	}
	fileChanges, err := e.changedFiles(ctx)
	if err != nil {
		return err
	}
	if err := e.drain(ctx, fileChanges); err != nil {
		return err
	}
	e.warnMultipleProjects()
	return e.countPending(ctx)
}

// dirtyOnly is the cheap pass: no directory walk. Each dirty row's own
// file is checked against its base so a concurrent edit of THAT file is
// still merged, not overwritten.
func (e *engine) dirtyOnly(ctx context.Context) error {
	dirty, err := e.loadDirty(ctx)
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	e.bases = map[string]baseEntry{}
	e.scan = scanResult{files: map[string]fileStat{}, skipped: map[string]string{}}
	fileChanges := map[rowKey]fileChange{}
	for k := range dirty {
		rel := k.rel()
		var b baseEntry
		err := e.tx.QueryRowContext(ctx, `SELECT hash, size, mtime_ns, synced_at_ns FROM `+tblFiles+` WHERE path = ?`, rel).
			Scan(&b.hash, &b.size, &b.mtimeNs, &b.syncedAtNs)
		known := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lsync: load base %s: %w", rel, err)
		}
		if known {
			e.bases[rel] = b
		}
		full := filepath.Join(e.o.DataDir, filepath.FromSlash(rel))
		info, serr := os.Lstat(full)
		switch {
		case serr != nil && known:
			fileChanges[k] = fileChange{deleted: true}
		case serr != nil:
			// no file, no base: a brand-new row; nothing on the file side
		default:
			st := fileStat{size: info.Size(), mtimeNs: info.ModTime().UnixNano()}
			e.scan.files[rel] = st
			data, rerr := readRowFile(full)
			if rerr != nil {
				e.recordError(ctx, rel, ErrKindInvalid, rerr.Error())
				continue
			}
			if !known || hashBytes(data) != b.hash {
				fileChanges[k] = fileChange{bytes: data, stat: st}
			}
		}
	}
	if err := e.drain(ctx, fileChanges); err != nil {
		return err
	}
	return e.countPending(ctx)
}

// drain processes file-side changes plus every dirty row, repeating while
// the processing itself marks more rows dirty (natural-key merges, id
// rewrites), up to maxDrainRounds.
func (e *engine) drain(ctx context.Context, fileChanges map[rowKey]fileChange) error {
	for round := 0; round < maxDrainRounds; round++ {
		dirty, err := e.loadDirty(ctx)
		if err != nil {
			return err
		}
		keys := map[rowKey]bool{}
		for k := range dirty {
			if !e.deferred[k] {
				keys[k] = true
			}
		}
		for k := range fileChanges {
			keys[k] = true
		}
		if len(keys) == 0 {
			break
		}
		for _, k := range sortedKeys(keys) {
			fc, hasFileChange := fileChanges[k]
			if err := e.syncOne(ctx, k, fc, hasFileChange, dirty[k]); err != nil {
				return err
			}
		}
		fileChanges = map[rowKey]fileChange{} // file side is fully consumed in round 0
		// Re-point rows imported after a merge in this round; rows the
		// rewrite changes are marked dirty and exported next round.
		for _, m := range e.idMerges {
			if err := rewriteIDEverywhere(ctx, e.tx, m[0], m[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileChange is the new state of a changed file; deleted ⇒ file is gone.
type fileChange struct {
	deleted bool
	bytes   []byte
	stat    fileStat
}

func (e *engine) loadScanAndBases(ctx context.Context) error {
	scan, err := scanDataDir(e.o.DataDir, e.reg, e.now)
	if err != nil {
		return err
	}
	e.scan = scan
	for rel, why := range scan.skipped {
		e.rep.Warnings = append(e.rep.Warnings, rel+": "+why)
	}
	sort.Strings(e.rep.Warnings)
	e.bases = map[string]baseEntry{}
	// The base BYTES are not loaded here: at 10k rows that is megabytes per
	// command for nothing. baseDoc loads one on demand.
	rows, err := e.tx.QueryContext(ctx, `SELECT path, hash, size, mtime_ns, synced_at_ns FROM `+tblFiles)
	if err != nil {
		return fmt.Errorf("lsync: load base index: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var b baseEntry
		if err := rows.Scan(&p, &b.hash, &b.size, &b.mtimeNs, &b.syncedAtNs); err != nil {
			return fmt.Errorf("lsync: scan base index: %w", err)
		}
		e.bases[p] = b
	}
	return rows.Err()
}

// baseBytes returns the canonical bytes last synced for rel (nil if none),
// loading them on first use.
func (e *engine) baseBytes(ctx context.Context, rel string) ([]byte, error) {
	b, ok := e.bases[rel]
	if !ok {
		return nil, nil
	}
	if b.base != nil {
		return b.base, nil
	}
	if err := e.tx.QueryRowContext(ctx, `SELECT base FROM `+tblFiles+` WHERE path = ?`, rel).Scan(&b.base); err != nil {
		return nil, fmt.Errorf("lsync: load base %s: %w", rel, err)
	}
	e.bases[rel] = b
	return b.base, nil
}

// changedFiles returns files whose content differs from the base index,
// plus indexed files that disappeared. The stat cache avoids reading
// unchanged files; the racy window forces a re-hash for files touched close
// to when they were recorded.
func (e *engine) changedFiles(ctx context.Context) (map[rowKey]fileChange, error) {
	out := map[rowKey]fileChange{}
	for rel, st := range e.scan.files {
		table, id, _ := parseRelPath(rel)
		b, known := e.bases[rel]
		racy := known && st.mtimeNs+int64(racyWindow) >= b.syncedAtNs
		if known && st.size == b.size && st.mtimeNs == b.mtimeNs && !racy {
			continue
		}
		data, err := readRowFile(filepath.Join(e.o.DataDir, filepath.FromSlash(rel)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			e.recordError(ctx, rel, ErrKindInvalid, err.Error())
			continue
		}
		if known && hashBytes(data) == b.hash {
			// Same content: refresh the stat cache (also ends the racy
			// window, so the file is not re-hashed on every pass).
			if err := e.touchBase(ctx, rel, st); err != nil {
				return nil, err
			}
			continue
		}
		out[rowKey{table, id}] = fileChange{bytes: data, stat: st}
	}
	for rel := range e.bases {
		if _, still := e.scan.files[rel]; still {
			continue
		}
		table, id, ok := parseRelPath(rel)
		if !ok {
			continue
		}
		if _, isTable := e.reg.Table(table); !isTable {
			continue
		}
		out[rowKey{table, id}] = fileChange{deleted: true}
	}
	return out, nil
}

// syncOne applies the §12.2 decision table to one row.
func (e *engine) syncOne(ctx context.Context, k rowKey, fc fileChange, fileChanged, dirty bool) error {
	t, ok := e.reg.Table(k.table)
	if !ok {
		return e.clearDirty(ctx, k)
	}
	rel := k.rel()
	_, hasBase := e.bases[rel]
	var baseDoc map[string]any
	if hasBase {
		raw, err := e.baseBytes(ctx, rel)
		if err != nil {
			return err
		}
		if d, err := decodeDoc(raw); err == nil {
			baseDoc = d
		}
	}

	// File side.
	var fileDoc map[string]any
	switch {
	case fileChanged && !fc.deleted:
		if len(fc.bytes) == 0 && hasBase {
			// A zero-length row file is never valid JSON; with a base on
			// record it is the debris of a power loss after an un-fsynced
			// export (writeFileAtomic). The DB still holds the row:
			// re-export it instead of reporting a broken file.
			return e.repairEmptyFile(ctx, k)
		}
		d, err := e.parseRowFile(ctx, rel, k, fc.bytes)
		if err != nil {
			e.deferred[k] = true
			return nil // reported; leave DB side + dirty flag untouched
		}
		fileDoc = d
	case !fileChanged && hasBase:
		fileDoc = baseDoc
	}

	// DB side.
	cols := liveColumns(t, e.present)
	dbDoc, err := readRowDoc(ctx, e.tx, t, cols, k.id)
	if err != nil {
		return err
	}
	dbChanged := false
	if dirty {
		withExtra, err := e.withStoredExtra(ctx, rel, dbDoc)
		if err != nil {
			return err
		}
		same, err := sameDoc(withExtra, baseDoc)
		if err != nil {
			return err
		}
		dbChanged = !same
	}

	kind := e.dbChangeKind()
	switch {
	case !fileChanged && !dbChanged:
		return e.clearDirty(ctx, k)
	case !fileChanged && dbChanged:
		if err := e.exportDoc(ctx, k, t, dbDoc); err != nil {
			return err
		}
	case fileChanged && !dbChanged:
		kind = ChangeImport
		if err := e.applyFile(ctx, k, t, fileDoc, fc); err != nil {
			return err
		}
	default:
		kind = ChangeMerge
		if err := e.bothChanged(ctx, k, t, baseDoc, fileDoc, dbDoc, fc); err != nil {
			return err
		}
	}
	return e.observe(ctx, k, t, kind, baseDoc)
}

// ChangeKind classifies a row change reported to Options.OnChange.
type ChangeKind string

const (
	// ChangeLocal: written by the lore command that is running.
	ChangeLocal ChangeKind = "write"
	// ChangeExternal: found in lore.db at the start of a command — written
	// since the last lore command by something other than lore's CLI path.
	ChangeExternal ChangeKind = "external-write"
	// ChangeImport: brought in from .lore/data (a pull, checkout, merge).
	ChangeImport ChangeKind = "import"
	// ChangeMerge: both sides changed; merged or a side chosen.
	ChangeMerge ChangeKind = "merge"
)

// Change is one row state transition reported to Options.OnChange. Hashes
// are DocHash values ("" = row absent).
type Change struct {
	Kind       ChangeKind
	Table      string
	ID         string
	BeforeHash string
	AfterHash  string
}

func (e *engine) dbChangeKind() ChangeKind {
	if e.o.DBChangeKind == "" {
		return ChangeLocal
	}
	return e.o.DBChangeKind
}

// observe reports the row's transition from its last synced state to its
// state after this pass, unless nothing changed or the export was deferred
// (it is reported once, by the pass that finally exports it).
func (e *engine) observe(ctx context.Context, k rowKey, t *Table, kind ChangeKind, baseDoc map[string]any) error {
	if e.o.OnChange == nil || e.deferred[k] {
		return nil
	}
	after, err := readRowDoc(ctx, e.tx, t, liveColumns(t, e.present), k.id)
	if err != nil {
		return err
	}
	before, err := DocHash(baseDoc)
	if err != nil {
		return err
	}
	afterHash, err := DocHash(after)
	if err != nil {
		return err
	}
	if before == afterHash {
		return nil
	}
	return e.o.OnChange(ctx, e.tx, Change{Kind: kind, Table: k.table, ID: k.id, BeforeHash: before, AfterHash: afterHash})
}

// DocHash is the content hash of a row document, ignoring updated_at (a
// bump that changes nothing else is not a change). nil → "".
func DocHash(doc map[string]any) (string, error) {
	if doc == nil {
		return "", nil
	}
	c := make(map[string]any, len(doc))
	for k, v := range doc {
		if k != updatedAtColumn {
			c[k] = v
		}
	}
	b, err := encodeDoc(c, nil)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}

// repairEmptyFile rewrites a zero-length row file from the DB.
func (e *engine) repairEmptyFile(ctx context.Context, k rowKey) error {
	t, _ := e.reg.Table(k.table)
	dbDoc, err := readRowDoc(ctx, e.tx, t, liveColumns(t, e.present), k.id)
	if err != nil {
		return err
	}
	e.rep.Warnings = append(e.rep.Warnings, k.rel()+": empty file (interrupted write) restored from lore.db")
	full := filepath.Join(e.o.DataDir, filepath.FromSlash(k.rel()))
	if err := removeRowFile(full); err != nil {
		return err
	}
	return e.exportDoc(ctx, k, t, dbDoc)
}

// bothChanged handles a row changed in the DB AND in its file since the base.
func (e *engine) bothChanged(ctx context.Context, k rowKey, t *Table, baseDoc, fileDoc, dbDoc map[string]any, fc fileChange) error {
	rel := k.rel()
	same, err := sameDoc(fileDoc, dbDoc)
	if err != nil {
		return err
	}
	switch {
	case same:
		return e.applyFile(ctx, k, t, fileDoc, fc)
	case fileDoc == nil:
		e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptDB, Why: "file deleted while the row was edited locally; kept the edit"})
		return e.exportDoc(ctx, k, t, dbDoc)
	case dbDoc == nil:
		e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptFile, Why: "row deleted locally while its file changed; kept the file"})
		return e.applyFile(ctx, k, t, fileDoc, fc)
	}
	newer := merge3.SideTheirs // theirs = file
	if firstIsNewer(dbDoc, fileDoc) {
		newer = merge3.SideOurs
	}
	res, err := merge3.Merge(baseDoc, dbDoc, fileDoc, merge3.Policy{StrategyFor: StrategyFor(t), Newer: newer})
	if err != nil {
		return err
	}
	if len(res.Conflicts) > 0 {
		// Text fields clashed: the file wins (it is what the team sees in
		// git); the local version is kept as a conflict copy.
		if err := e.saveConflictCopy(ctx, k, KeptFile, dbDoc); err != nil {
			return err
		}
		e.rep.Conflicts = append(e.rep.Conflicts, ConflictNote{Path: rel, Kept: KeptFile,
			Why: "both sides edited " + conflictKeys(res.Conflicts) + "; local version saved, see `lore sync conflicts`"})
		return e.applyFile(ctx, k, t, fileDoc, fc)
	}
	merged := res.Merged
	if err := e.importDoc(ctx, k, t, merged); err != nil {
		return e.importFailed(ctx, rel, err)
	}
	e.rep.Imported = append(e.rep.Imported, rel)
	return e.exportDoc(ctx, k, t, merged)
}

func conflictKeys(cs []merge3.Conflict) string {
	keys := make([]string, len(cs))
	for i, c := range cs {
		keys[i] = c.Key
	}
	return strings.Join(keys, ", ")
}

// StrategyFor returns the merge strategy per key for rows of t
// (LORE_SYNC_SPEC.md §9): updated_at takes the max, scalar state (enums,
// bools, numbers, timestamps, *_id references) takes the newer side, and
// free text / JSON clashes are loud conflicts.
// ds:def id=sync-strategyfor-ycedma28 owner=@khanakia stability=stable desc="per-field merge strategy"
func StrategyFor(t *Table) func(string) merge3.Strategy {
	return func(key string) merge3.Strategy {
		if key == updatedAtColumn {
			return merge3.StrategyMax
		}
		if key == keyVersion || key == keyTable {
			return merge3.StrategyMax
		}
		c, ok := t.column(key)
		if !ok {
			return merge3.StrategyConflict
		}
		switch c.Type {
		case field.TypeString:
			if strings.HasSuffix(key, "_id") {
				return merge3.StrategyNewest
			}
			return merge3.StrategyConflict
		case field.TypeJSON, field.TypeOther, field.TypeBytes:
			return merge3.StrategyConflict
		default:
			return merge3.StrategyNewest
		}
	}
}

// parseRowFile decodes and validates a row file; on failure it records the
// error and returns it.
//
// One failure still imports: conflict markers inside a value of an otherwise
// valid file. That is the merge driver's output when both branches changed
// the same text field (§9). Importing it lands every field that merged
// cleanly and shows the person both versions in `lore <entity> show`; their
// `lore <entity> edit` then exports a settled file over it. Blocking it
// instead would leave lore unable to settle its own conflict: the edit would
// stay dirty behind a file it may never overwrite. The notice stays in the
// report and status, and the pre-commit check refuses the markers, until the
// field is settled.
func (e *engine) parseRowFile(ctx context.Context, rel string, k rowKey, b []byte) (map[string]any, error) {
	kind, detail := validateRowFile(b, k.table, k.id)
	if kind == "" {
		return decodeDoc(b)
	}
	if kind == ErrKindConflict {
		if doc, err := decodeDoc(b); err == nil {
			e.record(ctx, rel, kind, detail+" — settle it with a lore edit of that row, or by editing the file", false)
			e.unsettled[rel] = true
			return doc, nil
		}
	}
	e.recordError(ctx, rel, kind, detail)
	return nil, errors.New(detail)
}

// validateRowFile returns ("", "") for a valid row file of table/id, or an
// error kind and detail. Shared by the importer and CheckFiles so doctor and
// sync can never disagree about what "broken" means.
func validateRowFile(b []byte, table, id string) (string, string) {
	doc, err := decodeDoc(b)
	if err != nil {
		kind := ErrKindInvalid
		if errors.Is(err, errNewerFormat) {
			kind = ErrKindNewer
		}
		if merge3.ContainsConflictMarkers(string(b)) {
			kind = ErrKindConflict
		}
		return kind, err.Error()
	}
	if tbl, _ := doc[keyTable].(string); tbl != table {
		return ErrKindInvalid, fmt.Sprintf("file says _table %q but lives in %s/", tbl, table)
	}
	if got, _ := doc[idColumn].(string); got != id {
		return ErrKindInvalid, fmt.Sprintf("file id %q does not match its name %s", got, id)
	}
	for key, v := range doc {
		if s, ok := v.(string); ok && merge3.ContainsConflictMarkers(s) {
			return ErrKindConflict, fmt.Sprintf("unresolved conflict markers in %q", key)
		}
	}
	return "", ""
}

// applyFile makes the DB match a changed file (import or delete).
func (e *engine) applyFile(ctx context.Context, k rowKey, t *Table, fileDoc map[string]any, fc fileChange) error {
	rel := k.rel()
	if fc.deleted || fileDoc == nil {
		if err := e.deleteRowToTrash(ctx, k, t, "file deleted"); err != nil {
			return err
		}
		return e.dropBase(ctx, rel)
	}
	if err := e.importDoc(ctx, k, t, fileDoc); err != nil {
		return e.importFailed(ctx, rel, err)
	}
	e.rep.Imported = append(e.rep.Imported, rel)
	if !e.unsettled[rel] {
		e.clearError(ctx, rel)
	}
	if fc.bytes != nil {
		return e.setBase(ctx, k, fc.bytes, fc.stat)
	}
	return e.clearDirty(ctx, k)
}

func (e *engine) importFailed(ctx context.Context, rel string, err error) error {
	e.recordError(ctx, rel, ErrKindImport, err.Error())
	return nil
}

// importDoc upserts a document, resolving natural-key collisions (E2).
func (e *engine) importDoc(ctx context.Context, k rowKey, t *Table, doc map[string]any) error {
	ra, err := argsFromDoc(t, doc)
	if err != nil {
		return err
	}
	cols := liveColumns(t, e.present)
	vol := liveVolatile(t, e.present)
	err = upsertRow(ctx, e.tx, t, cols, vol, ra, e.now)
	if errors.Is(err, errUniqueViolation) {
		if merr := e.mergeNaturalKey(ctx, k, t, ra, doc); merr != nil {
			return merr
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := e.storeExtra(ctx, k.rel(), ra.extra); err != nil {
		return err
	}
	return e.clearDirty(ctx, k)
}

func liveVolatile(t *Table, present map[string]map[string]bool) []Column {
	var out []Column
	for _, c := range t.Volatile {
		if present[t.Name][c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// mergeNaturalKey resolves two rows that share a natural key but carry
// different ids. The lexicographically smaller id wins everywhere (a rule
// every machine applies identically, so all clones converge); references to
// the loser are rewritten in every table and the loser's file is removed.
// The surviving row takes the content of whichever side is newer.
func (e *engine) mergeNaturalKey(ctx context.Context, k rowKey, t *Table, ra rowArgs, doc map[string]any) error {
	for _, key := range t.NaturalKeys {
		other, err := findByNaturalKey(ctx, e.tx, t, key, ra)
		if err != nil {
			return err
		}
		if other == "" {
			continue
		}
		cols := liveColumns(t, e.present)
		otherDoc, err := readRowDoc(ctx, e.tx, t, cols, other)
		if err != nil {
			return err
		}
		winner, loser := other, k.id
		if k.id < other {
			winner, loser = k.id, other
		}
		if err := rewriteIDEverywhere(ctx, e.tx, loser, winner); err != nil {
			return err
		}
		e.idMerges = append(e.idMerges, [2]string{loser, winner})
		content := doc
		if otherDoc != nil && !firstIsNewer(doc, otherDoc) {
			content = otherDoc
		}
		content = withID(content, winner)
		wra, err := argsFromDoc(t, content)
		if err != nil {
			return err
		}
		if err := upsertRow(ctx, e.tx, t, cols, liveVolatile(t, e.present), wra, e.now); err != nil {
			return fmt.Errorf("lsync: merge natural key %s: %w", t.Name, err)
		}
		e.rep.Merged = append(e.rep.Merged, fmt.Sprintf("%s: %s merged into %s (same %s)", t.Name, loser, winner, strings.Join(key, "+")))
		// Both ids must be re-examined: the winner's row changed, the
		// loser's row is gone and its file must go too.
		for _, id := range []string{winner, loser} {
			if _, err := e.tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+tblDirty+`(table_name, row_id) VALUES (?, ?)`, t.Name, id); err != nil {
				return fmt.Errorf("lsync: mark dirty: %w", err)
			}
		}
		if loser == k.id {
			// The incoming file is the loser: drop it now so the next round
			// does not re-import it.
			if err := removeRowFile(filepath.Join(e.o.DataDir, filepath.FromSlash(k.rel()))); err != nil {
				return err
			}
			e.rep.Removed = append(e.rep.Removed, k.rel())
			if err := e.dropBase(ctx, k.rel()); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%w on %s but no natural-key twin found", errUniqueViolation, t.Name)
}

func withID(doc map[string]any, id string) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	out[idColumn] = id
	return out
}

// exportDoc writes the DB row's document to its file (or removes the file
// when the row is gone).
func (e *engine) exportDoc(ctx context.Context, k rowKey, t *Table, dbDoc map[string]any) error {
	rel := k.rel()
	full := filepath.Join(e.o.DataDir, filepath.FromSlash(rel))
	if e.o.ReadOnly {
		e.deferred[k] = true
		return nil // stays dirty; exported by the next writable pass
	}
	if e.blocked[rel] {
		// The file on disk could not be parsed (half-resolved conflict,
		// newer format, hand edit). Never overwrite it: the user's text
		// would be lost. The row stays dirty until the file is fixed.
		e.deferred[k] = true
		return nil
	}
	if dbDoc == nil {
		if _, err := os.Lstat(full); err == nil {
			if err := removeRowFile(full); err != nil {
				return err
			}
			e.rep.Removed = append(e.rep.Removed, rel)
		}
		if err := e.dropBase(ctx, rel); err != nil {
			return err
		}
		return e.clearDirty(ctx, k)
	}
	extra, err := e.loadExtra(ctx, rel)
	if err != nil {
		return err
	}
	b, err := encodeDoc(dbDoc, extra)
	if err != nil {
		return err
	}
	if e.o.CheckSecrets != nil {
		if why := e.o.CheckSecrets(b); why != "" {
			e.recordError(ctx, rel, ErrKindSecret, why+" — not written to git; edit the row or set LORE_SYNC_ALLOW_SECRETS=1 for one run")
			e.deferred[k] = true
			return nil // stays dirty so it is retried (and reported) every pass
		}
	}
	if cur, err := os.ReadFile(full); err == nil && string(cur) == string(b) {
		// Already identical on disk (e.g. merged content written earlier).
	} else {
		if err := writeFileAtomic(full, b); err != nil {
			return err
		}
		e.rep.Exported = append(e.rep.Exported, rel)
	}
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("lsync: stat after write: %w", err)
	}
	e.clearError(ctx, rel)
	return e.setBase(ctx, k, b, fileStat{size: info.Size(), mtimeNs: info.ModTime().UnixNano()})
}

// deleteRowToTrash copies a row to the trash table, then deletes it.
func (e *engine) deleteRowToTrash(ctx context.Context, k rowKey, t *Table, reason string) error {
	cols := liveColumns(t, e.present)
	doc, err := readRowDoc(ctx, e.tx, t, cols, k.id)
	if err != nil {
		return err
	}
	if doc == nil {
		return e.clearDirty(ctx, k)
	}
	b, err := encodeDoc(doc, nil)
	if err != nil {
		return err
	}
	if _, err := e.tx.ExecContext(ctx,
		`INSERT INTO `+tblTrash+`(table_name, row_id, doc, reason, at) VALUES (?, ?, ?, ?, ?)`,
		k.table, k.id, string(b), reason, e.now.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("lsync: trash %s: %w", k.rel(), err)
	}
	if err := deleteRowByID(ctx, e.tx, t, k.id); err != nil {
		return err
	}
	e.rep.Deleted = append(e.rep.Deleted, k.rel())
	return e.clearDirty(ctx, k)
}

func (e *engine) saveConflictCopy(ctx context.Context, k rowKey, kept KeptSide, other map[string]any) error {
	b, err := encodeDoc(other, nil)
	if err != nil {
		return err
	}
	if _, err := e.tx.ExecContext(ctx,
		`INSERT INTO `+tblConflicts+`(table_name, row_id, kept, other_doc, at) VALUES (?, ?, ?, ?, ?)`,
		k.table, k.id, string(kept), string(b), e.now.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("lsync: save conflict copy: %w", err)
	}
	return nil
}

// ------------------------------------------------------------- bookkeeping

func (e *engine) setBase(ctx context.Context, k rowKey, b []byte, st fileStat) error {
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
	return e.clearDirty(ctx, k)
}

func (e *engine) touchBase(ctx context.Context, rel string, st fileStat) error {
	if _, err := e.tx.ExecContext(ctx, `UPDATE `+tblFiles+` SET size = ?, mtime_ns = ?, synced_at_ns = ? WHERE path = ?`,
		st.size, st.mtimeNs, e.now.UnixNano(), rel); err != nil {
		return fmt.Errorf("lsync: touch base %s: %w", rel, err)
	}
	return nil
}

func (e *engine) dropBase(ctx context.Context, rel string) error {
	if _, err := e.tx.ExecContext(ctx, `DELETE FROM `+tblFiles+` WHERE path = ?`, rel); err != nil {
		return fmt.Errorf("lsync: drop base %s: %w", rel, err)
	}
	if _, err := e.tx.ExecContext(ctx, `DELETE FROM `+tblExtra+` WHERE path = ?`, rel); err != nil {
		return fmt.Errorf("lsync: drop extra %s: %w", rel, err)
	}
	delete(e.bases, rel)
	return nil
}

func (e *engine) loadDirty(ctx context.Context) (map[rowKey]bool, error) {
	rows, err := e.tx.QueryContext(ctx, `SELECT table_name, row_id FROM `+tblDirty)
	if err != nil {
		return nil, fmt.Errorf("lsync: load dirty: %w", err)
	}
	defer rows.Close()
	out := map[rowKey]bool{}
	for rows.Next() {
		var k rowKey
		if err := rows.Scan(&k.table, &k.id); err != nil {
			return nil, fmt.Errorf("lsync: scan dirty: %w", err)
		}
		out[k] = true
	}
	return out, rows.Err()
}

func (e *engine) clearDirty(ctx context.Context, k rowKey) error {
	if _, err := e.tx.ExecContext(ctx, `DELETE FROM `+tblDirty+` WHERE table_name = ? AND row_id = ?`, k.table, k.id); err != nil {
		return fmt.Errorf("lsync: clear dirty: %w", err)
	}
	return nil
}

func (e *engine) storeExtra(ctx context.Context, rel string, extra map[string]any) error {
	if len(extra) == 0 {
		_, err := e.tx.ExecContext(ctx, `DELETE FROM `+tblExtra+` WHERE path = ?`, rel)
		if err != nil {
			return fmt.Errorf("lsync: clear extra: %w", err)
		}
		return nil
	}
	b, err := encodeDoc(extra, nil)
	if err != nil {
		return err
	}
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+tblExtra+`(path, doc) VALUES (?, ?)
		ON CONFLICT(path) DO UPDATE SET doc = excluded.doc`, rel, string(b)); err != nil {
		return fmt.Errorf("lsync: store extra: %w", err)
	}
	return nil
}

func (e *engine) loadExtra(ctx context.Context, rel string) (map[string]any, error) {
	var s string
	err := e.tx.QueryRowContext(ctx, `SELECT doc FROM `+tblExtra+` WHERE path = ?`, rel).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lsync: load extra: %w", err)
	}
	doc, err := decodeExtra(s)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// recordError stores a per-file error (replacing any previous one) and adds
// it to the report. A failure to record is itself only reported: losing the
// bookkeeping row must not abort the pass.
func (e *engine) recordError(ctx context.Context, rel, kind, detail string) {
	e.record(ctx, rel, kind, detail, kind != ErrKindSecret)
}

// record stores a per-file error; block keeps exports from overwriting the
// file this pass (a file that could not be read must never be replaced).
func (e *engine) record(ctx context.Context, rel, kind, detail string, block bool) {
	e.rep.Errors = append(e.rep.Errors, FileError{Path: rel, Kind: kind, Detail: detail})
	if block {
		e.blocked[rel] = true
	}
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+tblErrors+`(path, kind, detail, at) VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET kind = excluded.kind, detail = excluded.detail, at = excluded.at`,
		rel, kind, detail, e.now.Format(time.RFC3339)); err != nil {
		e.rep.Warnings = append(e.rep.Warnings, "could not record sync error: "+err.Error())
	}
}

func (e *engine) clearError(ctx context.Context, rel string) {
	if _, err := e.tx.ExecContext(ctx, `DELETE FROM `+tblErrors+` WHERE path = ?`, rel); err != nil {
		e.rep.Warnings = append(e.rep.Warnings, "could not clear sync error: "+err.Error())
	}
}

// countPending reports DB changes still waiting for export.
func (e *engine) countPending(ctx context.Context) error {
	var n int
	if err := e.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tblDirty).Scan(&n); err != nil {
		return fmt.Errorf("lsync: count dirty: %w", err)
	}
	e.rep.PendingExports = n
	return nil
}

// warnMultipleProjects flags E1: two bootstraps that met in a merge.
func (e *engine) warnMultipleProjects() {
	n := CountRowFiles(e.o.DataDir, projectsTable)
	if n > 1 {
		e.rep.Warnings = append(e.rep.Warnings, fmt.Sprintf(
			"%d project files in .lore/data/projects (parallel bootstraps met in a merge); run `lore sync fix-projects`", n))
	}
}

func sortedKeys(m map[rowKey]bool) []rowKey {
	out := make([]rowKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].table != out[j].table {
			return out[i].table < out[j].table
		}
		return out[i].id < out[j].id
	})
	return out
}

// CountRowFiles counts row files of one table on disk (in-flight tmp files
// excluded). Doctor uses it to flag several project files (E1).
func CountRowFiles(dataDir, table string) int {
	entries, err := os.ReadDir(filepath.Join(dataDir, table))
	if err != nil {
		return 0
	}
	n := 0
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), rowFileExt) && !strings.HasPrefix(en.Name(), tmpPrefix) {
			n++
		}
	}
	return n
}

// withStoredExtra returns doc plus any preserved unknown fields of rel, so
// it compares equal to a base that was written with them.
func (e *engine) withStoredExtra(ctx context.Context, rel string, doc map[string]any) (map[string]any, error) {
	if doc == nil {
		return nil, nil
	}
	extra, err := e.loadExtra(ctx, rel)
	if err != nil || len(extra) == 0 {
		return doc, err
	}
	out := make(map[string]any, len(doc)+len(extra))
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range doc {
		out[k] = v
	}
	return out, nil
}

// decodeExtra parses a stored extras object.
func decodeExtra(s string) (map[string]any, error) {
	return canonjson.Decode([]byte(s), canonjson.DefaultMaxDepth)
}

// sameDoc compares two documents ignoring updated_at; nil equals only nil.
func sameDoc(a, b map[string]any) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	return sameIgnoringUpdatedAt(a, b)
}
