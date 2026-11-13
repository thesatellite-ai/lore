package rowfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Folder is one data folder of a store (<root>/<name>, its rows at
// <name>/<table>/<id>.json; a code repo's <top>/.lore/data). It reads and
// writes row files; locking and the read-only rule belong to the backend
// that owns it.
type Folder struct {
	root    *Root
	name    string // clean folder name, FolderRoot for the root
	layout  Layout // where its rows sit (the root's, or LayoutRepo for an old-layout store folder)
	rel     string // data dir relative to the root, slash-separated ("." a store's root)
	dataDir string // absolute data dir
	// metaSource, when set, is asked for the shared copy of the folder's
	// _meta.json before a first write creates one (SetMetaSource).
	metaSource MetaSource
	// memo keeps the rows Read decoded (readmemo.go); a Folder is only
	// used through its pointer, so the memo is never copied.
	memo readMemo
	// badMu guards bad: the files the last Read left out because they are
	// not usable rows (ReadProblems).
	badMu sync.Mutex
	bad   []Problem
}

// ReadProblems lists the row files the last Read left out because they
// cannot be used (not JSON, wrong path, over the cap, unresolved conflict
// markers): fail closed, never a silent gap (STORE_SERVER_SPEC merge rule
// 2, v0.1.17's "invalid-file" line). A broken file never enters the
// cursor, so every Read checks it again and the list is always complete;
// it is cheap (no extra file access) and empty before the first Read.
func (f *Folder) ReadProblems() []Problem {
	f.badMu.Lock()
	defer f.badMu.Unlock()
	return slices.Clone(f.bad)
}

// MetaSource returns the shared (remote) copy of a store file, by its path
// relative to the store root; found=false when the shared side has none.
// A git backend sets one so a folder's first write in this clone adopts
// the _meta.json another clone or `lore org link` already pushed, instead
// of creating a second one that can never merge cleanly.
type MetaSource func(ctx context.Context, rel string) (doc []byte, found bool, err error)

// SetMetaSource installs src (nil removes it). Not safe to call while the
// folder is being written; backends call it once at open.
func (f *Folder) SetMetaSource(src MetaSource) { f.metaSource = src }

// Name is the clean folder name (FolderRoot for the root folder).
func (f *Folder) Name() string { return f.name }

// DataPath is the data dir relative to the store root, slash-separated:
// "." (a store's root folder), "org", "team/web"; ".lore/data" in a code
// repo. In a store it also holds the folders below it: join with path.Join
// and give git Pathspecs, never DataPath itself.
func (f *Folder) DataPath() string { return f.rel }

// Pathspecs are the git pathspecs holding exactly this folder's row files
// (Layout.Pathspecs).
func (f *Folder) Pathspecs() []string { return f.layout.Pathspecs(f.name) }

// Layout is where this folder's rows sit.
func (f *Folder) Layout() Layout { return f.layout }

// OldLayout reports whether this folder of a store is still in the layout
// stores had before the plain-files round (<folder>/.lore/data): it is
// read and written there until it is moved (brief 1.6). Always false for
// a code repo, whose rows belong in .lore/data.
func (f *Folder) OldLayout() bool { return f.root.layout == LayoutStore && f.layout == LayoutRepo }

// FileRel is a row file's path relative to this folder's data dir when the
// root-relative slash path p is one of this folder's row files.
func (f *Folder) FileRel(p string) (string, bool) {
	folder, rel, ok := f.layout.SplitRowPath(p)
	if !ok || folder != f.name {
		return "", false
	}
	return rel, true
}

// storePath is the root-relative slash path of rel under the data dir.
func (f *Folder) storePath(rel string) string { return path.Join(f.rel, rel) }

// Root is the store this folder belongs to.
func (f *Folder) Root() *Root { return f.root }

// StorePath is a row file's path relative to the store root,
// slash-separated (what git sees).
func (f *Folder) StorePath(table row.Table, id ids.AnyID) string {
	return f.storePath(RowPath(table, id))
}

// Problem is one entry of a data folder that is not a usable row file,
// reported by doctor and never imported. Path is relative to the data dir.
type Problem struct {
	Path   string
	Reason string
	// Code is the error code of a file whose bytes did not make a row
	// (E_OLD_LORE for a row written by a newer lore, which `lore migrate
	// status` counts as "newer"); "" for a stray entry (a link, a bad name).
	Code errs.Code
	// FieldMarkers: the file decodes as a row but a text field or meta
	// entry holds unresolved conflict markers. Lists and search leave it
	// out; Get (show) still reads it as it is, so it can be settled.
	FieldMarkers bool
}

// DecodeRowFile turns the bytes of the row file at rel (relative to a data
// dir, "table/id.json") into a backend Row. The document must name the
// same table and id as its path: a file copied under another name would
// otherwise shadow a different row.
func DecodeRowFile(rel string, doc []byte) (store.Row, error) {
	table, id, ok := ParseRowPath(rel)
	if !ok {
		return store.Row{}, errs.New(errs.BadPath, "rowfs: "+quote(rel)+" is not <table>/<id>.json")
	}
	r, err := row.Decode(doc)
	if err != nil {
		return store.Row{}, err
	}
	if r.Table != table || r.ID != id {
		return store.Row{}, errs.New(errs.InvalidInput, "rowfs: "+quote(rel)+" holds the row "+quote(r.Table.String()+"/"+r.ID.String()))
	}
	return store.Row{Table: r.Table, ID: r.ID, HLC: r.HLC, Deleted: r.Deleted, Doc: doc}, nil
}

// Get reads one row; a tombstone is returned as a row, a missing file is
// store.ErrNotFound.
func (f *Folder) Get(table row.Table, id ids.AnyID) (store.Row, error) {
	if !table.Valid() || id.IsZero() {
		return store.Row{}, errs.New(errs.InvalidInput, "rowfs: get needs a table and an id")
	}
	r, ok, err := f.current(RowPath(table, id))
	if err != nil {
		return store.Row{}, err
	}
	if !ok {
		return store.Row{}, store.ErrNotFound
	}
	return r, nil
}

// current reads the row at rel; ok is false when the file does not exist.
func (f *Folder) current(rel string) (store.Row, bool, error) {
	return f.currentIn(rel, chainMemo{})
}

// currentIn is current with the batch's checked folders (chainMemo).
func (f *Folder) currentIn(rel string, chains chainMemo) (store.Row, bool, error) {
	if err := chains.check(f.root.dir, f.storePath(rel)); err != nil {
		return store.Row{}, false, err
	}
	b, err := readCapped(filepath.Join(f.dataDir, filepath.FromSlash(rel)), rel)
	if errors.Is(err, fs.ErrNotExist) {
		return store.Row{}, false, nil
	}
	if err != nil {
		return store.Row{}, false, err
	}
	r, err := DecodeRowFile(rel, b)
	if err != nil {
		return store.Row{}, false, err
	}
	return r, true, nil
}

// entry is one row file found by a scan.
type entry struct {
	size  int64
	mtime int64
}

// scan lists every row file of the data dir without following links, and
// every entry that is not one. A data dir that does not exist yet is empty.
func (f *Folder) scan() (map[string]entry, []Problem, error) {
	files := map[string]entry{}
	var problems []Problem
	if err := checkChain(f.root.dir, f.rel); err != nil {
		return nil, nil, err
	}
	tables, err := os.ReadDir(f.dataDir)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil, nil
	}
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "rowfs: list the data folder", err)
	}
	flat := f.layout == LayoutStore
	for _, t := range tables {
		name := t.Name()
		switch {
		case flat && !strings.HasPrefix(name, tmpPrefix) && !IsTableDir(name):
			// a store folder holds its table folders beside the folders
			// below it, its own files and a person's files: only a table
			// folder is lore's to read here (D-d)
			continue
		case IsOSJunk(name), name == MetaFileName, name == PurgedFileName:
			continue
		case strings.HasPrefix(name, tmpPrefix):
			removeStaleTmp(filepath.Join(f.dataDir, name))
			continue
		case t.Type()&fs.ModeSymlink != 0:
			problems = append(problems, Problem{Path: name, Reason: "symlink ignored"})
			continue
		case !t.IsDir():
			problems = append(problems, Problem{Path: name, Reason: "not a table folder"})
			continue
		}
		if _, err := row.ParseTable(name); err != nil {
			problems = append(problems, Problem{Path: name, Reason: "not a table name"})
			continue
		}
		p, err := f.scanTable(name, files)
		if err != nil {
			return nil, nil, err
		}
		problems = append(problems, p...)
	}
	return files, problems, nil
}

// scanTable adds the row files of one table folder to files.
func (f *Folder) scanTable(table string, files map[string]entry) ([]Problem, error) {
	dir := filepath.Join(f.dataDir, table)
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: list a table folder", err)
	}
	var problems []Problem
	for _, e := range list {
		name := e.Name()
		rel := table + "/" + name
		switch {
		case IsOSJunk(name):
			continue
		case strings.HasPrefix(name, tmpPrefix):
			removeStaleTmp(filepath.Join(dir, name))
			continue
		case e.Type()&fs.ModeSymlink != 0:
			problems = append(problems, Problem{Path: rel, Reason: "symlink ignored"})
			continue
		case !e.Type().IsRegular():
			problems = append(problems, Problem{Path: rel, Reason: "not a regular file"})
			continue
		}
		if _, _, ok := ParseRowPath(rel); !ok {
			problems = append(problems, Problem{Path: rel, Reason: "file name is not <valid id>.json"})
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed between ReadDir and Info
		}
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "rowfs: stat a row file", err)
		}
		files[rel] = entry{size: info.Size(), mtime: nanos(info.ModTime())}
	}
	return problems, nil
}

// Read returns every row whose file changed since cursor (a StatCursor),
// tombstones included, plus the next cursor (spec 5.2, 6.2). A file whose
// size or mtime differ, or whose mtime is inside the racy window, is
// re-hashed; only a changed hash is a changed row. A file that disappeared
// is NOT a delete (only tombstones delete): it is simply absent from the
// next cursor, and Missing reports it. An unreadable cursor reads
// everything.
func (f *Folder) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	at := nanos(f.root.now())
	prev, _ := ParseStatCursor(cursor)
	seen := prev.byPath()
	files, _, err := f.scan()
	if err != nil {
		return nil, "", err
	}
	next := StatCursor{At: at, Files: make([]FileStat, 0, len(files))}
	rels := sortedKeys(files)
	// Files are checked (read, hashed, decoded) in parallel and collected in
	// path order, so the result is the same as a sequential loop
	// (BUG-TIME-BENCH: decoding a store of big task rows one by one).
	results := make([]checked, len(rels))
	if err = parallel(ctx, len(rels), func(i int) {
		rel := rels[i]
		p, known := seen[rel]
		results[i].stat, results[i].row, results[i].ok, results[i].bad = f.check(rel, files[rel], p, known, prev.At, at)
	}); err != nil {
		return nil, "", err
	}
	var out []store.Row
	var bad []Problem
	for i, c := range results {
		if c.bad != nil {
			bad = append(bad, Problem{Path: rels[i], Reason: c.bad.Error(), Code: errs.CodeOf(c.bad), FieldMarkers: isFieldMarkers(c.bad)})
		}
		if !c.ok {
			continue
		}
		if c.row != nil {
			out = append(out, *c.row)
		}
		next.Files = append(next.Files, c.stat)
	}
	f.badMu.Lock()
	f.bad = bad
	f.badMu.Unlock()
	if cursor != "" && len(out) == 0 && settled(prev, next) {
		// B1-16: nothing changed since cursor and no file is racy under its
		// time, so a new cursor would say exactly what it says: it is the
		// answer as it is, and the caller keeps its cache untouched instead
		// of rewriting a 5,000-file cursor (and the cache file) every
		// command. While a file is racy the time moves on as before, so it
		// stops being re-hashed once it is past the window.
		return nil, cursor, nil
	}
	c, err := next.Encode()
	if err != nil {
		return nil, "", err
	}
	return out, c, nil
}

// settled reports whether next lists exactly prev's files with the same
// size, mtime and hash, none of them racy under prev's time.
func settled(prev, next StatCursor) bool {
	if len(prev.Files) != len(next.Files) {
		return false
	}
	seen := prev.byPath()
	for _, n := range next.Files {
		if p, ok := seen[n.Path]; !ok || p.Size != n.Size || p.MTime != n.MTime || p.SHA256 != n.SHA256 || racy(n.MTime, prev.At) {
			return false
		}
	}
	return true
}

// check decides one scanned file against its previous stat p (known is
// false for a file the cursor never saw). It returns the stat for the next
// cursor, the row when the file changed, and ok false when the file must
// stay out of the cursor (gone since the scan, or unreadable: Problems
// reports it, and leaving it out makes the next Read try again).
func (f *Folder) check(rel string, st entry, p FileStat, known bool, prevAt, at int64) (FileStat, *store.Row, bool, error) {
	if known && p.Size == st.size && p.MTime == st.mtime && !racy(st.mtime, prevAt) {
		return p, nil, true, nil
	}
	// An unchanged file this process already decoded (readmemo.go).
	if m, ok := f.memo.fresh(rel, st); ok {
		stat := FileStat{Path: rel, Size: m.size, MTime: st.mtime, SHA256: m.sha}
		if known && stat.SHA256 == p.SHA256 {
			return stat, nil, true, nil
		}
		r := m.row
		return stat, &r, true, nil
	}
	b, err := readCapped(filepath.Join(f.dataDir, filepath.FromSlash(rel)), rel)
	if errors.Is(err, fs.ErrNotExist) {
		return FileStat{}, nil, false, nil // gone since the scan: not a problem
	}
	if err != nil {
		return FileStat{}, nil, false, err
	}
	stat := FileStat{Path: rel, Size: int64(len(b)), MTime: st.mtime, SHA256: sum(b)}
	if known && stat.SHA256 == p.SHA256 {
		return stat, nil, true, nil
	}
	r, ok := f.memo.bySHA(rel, stat.SHA256)
	if !ok {
		if r.row, err = decodeImportable(rel, b); err != nil {
			return FileStat{}, nil, false, err
		}
	}
	f.memo.put(rel, memoRow{size: stat.Size, mtime: st.mtime, at: at, sha: stat.SHA256, row: r.row})
	decoded := r.row
	return stat, &decoded, true, nil
}

// Problems lists every entry of the data folder that is not a usable row:
// stray files, links, bad names, files over the cap, and documents that do
// not decode or do not match their path. For `lore doctor`.
func (f *Folder) Problems(ctx context.Context) ([]Problem, error) {
	files, problems, err := f.scan()
	if err != nil {
		return nil, err
	}
	for _, rel := range sortedKeys(files) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A file this process already decoded and that has not changed
		// since is a usable row (readmemo.go): doctor asks for problems
		// several times a run, and reading every file each time was most
		// of a doctor run on a big store (item 41).
		if _, ok := f.memo.fresh(rel, files[rel]); ok {
			continue
		}
		b, err := readCapped(filepath.Join(f.dataDir, filepath.FromSlash(rel)), rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			_, err = decodeImportable(rel, b)
		}
		if err != nil {
			problems = append(problems, Problem{Path: rel, Reason: err.Error(), Code: errs.CodeOf(err), FieldMarkers: isFieldMarkers(err)})
		}
	}
	return problems, nil
}

// Missing lists the row files a cursor saw that are gone from disk without
// a tombstone (paths relative to the data dir, sorted). They are never
// treated as deletes; doctor reports them (spec 6.2 Change detection).
// An unreadable cursor has seen nothing, so nothing is missing.
func (f *Folder) Missing(cursor store.Cursor) ([]string, error) {
	prev, ok := ParseStatCursor(cursor)
	if !ok {
		return nil, nil
	}
	files, _, err := f.scan()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range prev.Files {
		if _, there := files[p.Path]; !there {
			out = append(out, p.Path)
		}
	}
	slices.Sort(out)
	return out, nil
}

// ValidateBatch checks a batch before anything is read or written: every
// change names a table, an id, an HLC and a canonical document of at most
// MaxFileSize bytes whose envelope agrees with the header, and no row
// appears twice (store.ErrBatch).
func ValidateBatch(changes []store.Change) error {
	seen := make(map[store.Key]struct{}, len(changes))
	for _, c := range changes {
		r := c.Row
		if !r.Table.Valid() || r.ID.IsZero() || r.HLC.IsZero() || len(r.Doc) == 0 {
			return errs.New(errs.InvalidInput, "rowfs: a change without table, id, hlc or document")
		}
		if _, dup := seen[r.Key()]; dup {
			return errs.Wrap(errs.InvalidInput, "rowfs: batch", store.ErrBatch)
		}
		seen[r.Key()] = struct{}{}
		if len(r.Doc) > MaxFileSize {
			return tooLarge(RowPath(r.Table, r.ID), int64(len(r.Doc)))
		}
		canonical, err := canon.Document(r.Doc)
		if err != nil || !bytes.Equal(canonical, r.Doc) {
			return errs.New(errs.InvalidInput, "rowfs: a change document is not canonical JSON")
		}
		m, err := row.Decode(r.Doc)
		if err != nil {
			return err
		}
		if m.Table != r.Table || m.ID != r.ID || m.HLC.Compare(r.HLC) != 0 || m.Deleted != r.Deleted {
			return errs.New(errs.InvalidInput, "rowfs: a change header disagrees with its document")
		}
	}
	return nil
}

// Applied is a batch written to disk, kept so the caller can take it back
// (the git backend does when its commit fails).
type Applied struct {
	undo []undoStep
	// Paths are the written files relative to the store root,
	// slash-separated, in batch order.
	Paths []string
}

// AddCreated records a file this batch created beside its row files (the
// store marker a first write adds): it is committed with the batch, and
// Undo removes it again. full is its path on disk, rel the slash path
// from the store root.
func (a *Applied) AddCreated(full, rel string) {
	a.undo = append(a.undo, undoStep{full: full})
	a.Paths = append(a.Paths, rel)
}

// AddReplaced records a file this batch rewrote beside its row files (an
// earlier lore's README.md, rowfs.EnsureReadme): it is committed with the
// batch, and Undo writes prev back.
func (a *Applied) AddReplaced(full, rel string, prev []byte) {
	a.undo = append(a.undo, undoStep{full: full, prev: prev, m: fileModes{file: readmeMode, dir: sharedDirMode}})
	a.Paths = append(a.Paths, rel)
}

// undoStep restores one file: prev nil means the file did not exist.
type undoStep struct {
	full string
	prev []byte
	m    fileModes
}

// Undo restores every file of the batch to what it held before, newest
// first. It is the "none" of all-or-none.
func (a *Applied) Undo() error {
	var errList []error
	for i := len(a.undo) - 1; i >= 0; i-- {
		u := a.undo[i]
		if u.prev == nil {
			errList = append(errList, removeIfExists(u.full))
			continue
		}
		errList = append(errList, writeAtomic(u.full, u.prev, u.m))
	}
	a.undo = nil
	if err := errors.Join(errList...); err != nil {
		return errs.Wrap(errs.Internal, "rowfs: restore a failed batch", err)
	}
	return nil
}

// Apply writes a checked batch all or none. The caller holds the store lock
// (Root.Lock) and has refused read-only handles. Every Expect is checked
// against the file on disk first (zero = the row must not exist, else the
// stored HLC must equal it; store.ErrStale otherwise); then every file is
// written; a failure part way restores the files already replaced.
func (f *Folder) Apply(ctx context.Context, changes []store.Change) (*Applied, error) {
	if err := ValidateBatch(changes); err != nil {
		return nil, err
	}
	a := &Applied{Paths: make([]string, 0, len(changes))}
	prevs := make([][]byte, len(changes))
	chains := chainMemo{}
	for i, c := range changes {
		rel := RowPath(c.Row.Table, c.Row.ID)
		cur, exists, err := f.currentIn(rel, chains)
		if err != nil {
			return nil, err
		}
		switch {
		case c.Expect.IsZero() && exists:
			return nil, store.ErrStale
		case !c.Expect.IsZero() && (!exists || cur.HLC.Compare(c.Expect) != 0):
			return nil, store.ErrStale
		}
		if exists {
			prevs[i] = cur.Doc
		}
	}
	if err := f.ensureMeta(ctx, a); err != nil {
		return nil, errors.Join(err, a.Undo())
	}
	// FORMAT_EVOLUTION rule 2: the features the rows use are declared in the
	// same batch (same commit) as the rows.
	if err := f.declare(a, changes); err != nil {
		return nil, errors.Join(err, a.Undo())
	}
	// Each file is written, fsync'd and renamed into place on its own
	// goroutine (concurrent fsyncs share the file system's journal commits);
	// the folders are fsync'd once each afterwards. A batch of 5,000 rows in
	// one table folder was 10,000 fsyncs one after the other (owner item 27).
	fulls := make([]string, len(changes))
	for i, c := range changes {
		rel := RowPath(c.Row.Table, c.Row.ID)
		fulls[i] = filepath.Join(f.dataDir, filepath.FromSlash(rel))
		if err := chains.check(f.root.dir, f.storePath(rel)); err != nil {
			return nil, errors.Join(err, a.Undo())
		}
	}
	dirOf := make([]string, len(changes))
	werrs := make([]error, len(changes))
	perr := parallel(ctx, len(changes), func(i int) {
		dirOf[i], werrs[i] = writeRenamed(fulls[i], changes[i].Row.Doc, f.root.modes)
	})
	var dirs []string
	for i, c := range changes {
		if dirOf[i] == "" { // failed, or skipped by a cancelled ctx
			continue
		}
		if !slices.Contains(dirs, dirOf[i]) {
			dirs = append(dirs, dirOf[i])
		}
		a.undo = append(a.undo, undoStep{full: fulls[i], prev: prevs[i], m: f.root.modes})
		a.Paths = append(a.Paths, f.StorePath(c.Row.Table, c.Row.ID))
	}
	if perr != nil { // the ctx ended: as the sequential loop did, its error
		return nil, errors.Join(perr, a.Undo())
	}
	for _, err := range werrs { // the first failure in batch order, as before
		if err != nil {
			return nil, errors.Join(err, a.Undo())
		}
	}
	for _, dir := range dirs {
		if err := syncDir(dir); err != nil {
			return nil, errors.Join(errs.Wrap(errs.Internal, "rowfs: fsync a table folder", err), a.Undo())
		}
	}
	// The caller holds the store lock: any other tmp file in a folder this
	// batch wrote (or in the data folder, where _meta.json lives) is a dead
	// writer's debris, removed now whatever its age (sweepTmp).
	for _, dir := range append(dirs, f.dataDir) {
		sweepTmp(dir)
	}
	return a, nil
}

// chainMemo remembers the folders checkChain already passed within one
// batch (the caller holds the store lock, so they stay real folders): a
// batch of 5,000 rows in one table folder Lstats that folder's chain once,
// not 10,000 times. The file itself is still Lstat'd per row (readCapped,
// writeRenamed refuse a link there).
type chainMemo map[string]bool

// check is checkChain(root, rel) with rel's folder part remembered.
func (m chainMemo) check(root, rel string) error {
	dir := path.Dir(rel)
	if m[dir] {
		return nil
	}
	if err := checkChain(root, rel); err != nil {
		return err
	}
	m[dir] = true
	return nil
}

// ensureMeta gives a non-root folder its _meta.json when the batch is the
// folder's first write here (the file is missing), as part of the batch:
// undone with it, and in a.Paths so a git store commits it with the rows.
// It adopts the shared copy when the MetaSource has one (written by
// `lore org link`, v0.1.17, or another clone), byte for byte, so the two
// sides hold the same file. Otherwise the project id is natural (ids.Natural
// over the store's sto_ id, "projects" and the folder name), so two clones
// creating the same folder independently write the same bytes. An existing
// local _meta.json is never touched. The root folder's identity is
// lore-store.yaml, so it gets none.
func (f *Folder) ensureMeta(ctx context.Context, a *Applied) error {
	if f.name == FolderRoot {
		return nil
	}
	full := filepath.Join(f.dataDir, MetaFileName)
	rel := f.storePath(MetaFileName)
	if err := checkChain(f.root.dir, rel); err != nil {
		return err
	}
	if _, err := os.Lstat(full); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errs.Wrap(errs.Internal, "rowfs: stat "+MetaFileName, err)
	}
	doc, err := f.newMeta(ctx, rel)
	if err != nil {
		return err
	}
	if err := writeAtomic(full, doc, f.root.modes); err != nil {
		return err
	}
	a.undo = append(a.undo, undoStep{full: full, m: f.root.modes})
	a.Paths = append(a.Paths, rel)
	return nil
}

// metaNamespaceTable is the table part of a folder project's natural id
// (as engine natural ids hash namespace, table, key).
const metaNamespaceTable = "projects"

// newMeta is the _meta.json bytes a first write creates: the shared copy
// when there is one, else a natural project id.
func (f *Folder) newMeta(ctx context.Context, rel string) ([]byte, error) {
	if f.metaSource != nil {
		shared, found, err := f.metaSource(ctx, rel)
		if err != nil {
			return nil, err
		}
		if found {
			if _, err := canon.Document(shared); err != nil {
				return nil, errs.Wrap(errs.InvalidInput, "rowfs: the shared "+rel+" is not JSON", err)
			}
			return shared, nil
		}
	}
	parts := []string{metaNamespaceTable, f.name}
	if m, err := ReadMarker(f.root.dir); err == nil && !m.ID.IsZero() {
		parts = append([]string{m.ID.String()}, parts...)
	}
	id, err := ids.Natural(ids.PrefixProject, parts...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: folder project id", err)
	}
	raw, err := json.Marshal(folderMeta{ProjectID: id.String(), Version: row.FormatVersion})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode "+MetaFileName, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode "+MetaFileName, err)
	}
	return doc, nil
}

// folderMeta is the _meta.json a folder's first write creates (spec 6.1: "a
// project folder (prj_ id in its _meta.json)"). Its json tags `project_id`
// and `_v` are the keys v0.1.17's stores package reads and keeps, so they
// must not change. A typed struct instead of a map keeps the file's shape
// closed (no `any`); canon.Document sorts the keys either way.
type folderMeta struct {
	ProjectID string `json:"project_id"`
	Version   int    `json:"_v"`
}

// sortedKeys returns m's keys in order, so reads are deterministic.
func sortedKeys(m map[string]entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
