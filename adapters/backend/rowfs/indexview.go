package rowfs

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// IndexView is what the version control of a data folder knows about its
// files without lore opening or stat-ing any of them (perf3, D-110): a warm
// read of a 5,500-row repo used to lstat every row file to learn that
// nothing had changed, which costs seconds of system time on macOS APFS.
// Paths are relative to the data folder, slash-separated.
type IndexView struct {
	// Clean maps every tracked regular file the work tree holds exactly as
	// the index has it to its blob id: same blob id, same bytes.
	Clean map[string]string
	// Check lists the files lore must look at itself: changed or deleted in
	// the work tree, not tracked (ignored ones too), or not a regular file
	// in the index. A path may name a file that is gone.
	Check []string
}

// IndexSource returns the folder's IndexView now, of the whole folder
// (scope "") or of one table folder (scope = the table's name, its paths
// still relative to the data folder): git then checks only that folder's
// files, so a read of a small table does not pay for the big ones. ok is
// false when there is none to trust (no git, a merge in progress, any git
// failure), and the read then walks the folder as before.
type IndexSource func(ctx context.Context, scope string) (view IndexView, ok bool)

// SetIndexSource makes Read and Missing trust src instead of stat-ing every
// row file (the git backend in a code repo sets it). Only a folder of the
// repo layout uses it: a store folder's flat layout mixes table folders
// with others, and its clone is read by commit anyway.
func (f *Folder) SetIndexSource(src IndexSource) {
	f.viewMu.Lock()
	defer f.viewMu.Unlock()
	f.index = src
	f.viewOK = false
}

// writeGen counts the files this process wrote through rowfs (every
// write and row file removal): a remembered IndexView is used only while it
// is unchanged, so lore's own writes are read back at once, whichever
// Folder or code path made them.
var writeGen atomic.Uint64

// WriteGeneration is the number of file writes this process made through
// rowfs so far: an answer about the data folder remembered under one value
// is out of date under another (the git backend's status memo).
func WriteGeneration() uint64 { return writeGen.Load() }

// indexView is f's IndexView of the whole folder when one can be trusted
// now. Inside a store.ReadScope (one command) the view is remembered while
// no file was written through rowfs (writeGen): a command reads its store
// several times (a doctor run ~5), and git checks every tracked file's
// stat each time it is asked. Files the view names as changed are still
// stat-ed on every read.
func (f *Folder) indexView(ctx context.Context) (IndexView, bool) {
	if f.layout != LayoutRepo {
		return IndexView{}, false
	}
	f.viewMu.Lock()
	defer f.viewMu.Unlock()
	if f.index == nil {
		return IndexView{}, false
	}
	scope := store.ReadScopeOf(ctx)
	if f.viewFresh(scope) {
		return f.view, true
	}
	gen := writeGen.Load()
	view, ok := f.index(ctx, "")
	f.view, f.viewOK, f.viewScope, f.viewGen = view, ok, scope, gen
	return view, ok
}

// viewFresh reports whether the remembered whole-folder view still holds
// in scope; the caller holds viewMu.
func (f *Folder) viewFresh(scope *store.ReadScope) bool {
	return f.viewOK && scope != nil && f.viewScope == scope && f.viewGen == writeGen.Load()
}

// tableView is the IndexView of table's folder alone: cut from the
// remembered whole-folder view while it holds (same command, nothing
// written), else asked of git for that
// folder only (not remembered: one list reads its table once).
func (f *Folder) tableView(ctx context.Context, table row.Table) (IndexView, bool) {
	if f.layout != LayoutRepo {
		return IndexView{}, false
	}
	f.viewMu.Lock()
	src, fresh, whole := f.index, f.viewFresh(store.ReadScopeOf(ctx)), f.view
	f.viewMu.Unlock()
	if src == nil {
		return IndexView{}, false
	}
	if !fresh {
		v, ok := src(ctx, string(table))
		if !ok {
			return IndexView{}, false
		}
		whole = v
	}
	prefix := string(table) + "/"
	sub := IndexView{Clean: map[string]string{}}
	for rel, blob := range whole.Clean {
		if strings.HasPrefix(rel, prefix) {
			sub.Clean[rel] = blob
		}
	}
	for _, rel := range whole.Check {
		if strings.HasPrefix(rel, prefix) {
			sub.Check = append(sub.Check, rel)
		}
	}
	return sub, true
}

// rowFileRel reports whether rel names a file a scan would list as a row
// file: <table>/<valid id>.json directly under a table folder, not OS junk
// and not one of lore's own temporary files.
func rowFileRel(rel string) bool {
	table, name, ok := strings.Cut(rel, "/")
	if !ok || strings.Contains(name, "/") || IsOSJunk(name) || strings.HasPrefix(name, tmpPrefix) {
		return false
	}
	if _, err := row.ParseTable(table); err != nil {
		return false
	}
	_, _, ok = ParseRowPath(rel)
	return ok
}

// indexedFile is one row file of an indexed read: blob is its index blob id
// when git reports it clean ("" for a file lore checks itself).
type indexedFile struct {
	rel  string
	blob string
}

// indexedFiles lists the row files of view, sorted by path. A file to
// check that is gone or is not a regular file is left out, as a scan
// leaves it out; its stat is returned for check.
func (f *Folder) indexedFiles(view IndexView) ([]indexedFile, map[string]entry, error) {
	stats := map[string]entry{}
	byRel := map[string]string{}
	for rel, blob := range view.Clean {
		if rowFileRel(rel) {
			byRel[rel] = blob
		}
	}
	for _, rel := range view.Check {
		if !rowFileRel(rel) {
			continue
		}
		delete(byRel, rel)
		info, err := os.Lstat(filepath.Join(f.dataDir, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, errs.Wrap(errs.Internal, "rowfs: stat a row file", err)
		}
		if !info.Mode().IsRegular() {
			continue // a link or a folder: a full scan reports it (Problems)
		}
		stats[rel] = entry{size: info.Size(), mtime: nanos(info.ModTime())}
		byRel[rel] = ""
	}
	out := make([]indexedFile, 0, len(byRel))
	for _, rel := range slices.Sorted(maps.Keys(byRel)) {
		out = append(out, indexedFile{rel: rel, blob: byRel[rel]})
	}
	return out, stats, nil
}

// readIndexed is Read under an IndexView: a file git reports clean whose
// blob id the cursor recorded is the cursor's entry as it is, with no file
// access; every other file is checked as a scan checks it. ok is false
// when the view cannot be used for this cursor and the caller must walk.
func (f *Folder) readIndexed(ctx context.Context, cursor store.Cursor, prev StatCursor, view IndexView, at int64) ([]store.Row, store.Cursor, error) {
	files, stats, err := f.indexedFiles(view)
	if err != nil {
		return nil, "", err
	}
	seen := prev.byPath()
	results := make([]checked, len(files))
	reused := make([]bool, len(files))
	if err := parallel(ctx, len(files), func(i int) {
		fi := files[i]
		p, known := seen[fi.rel]
		if fi.blob != "" && known && p.Blob == fi.blob {
			results[i] = checked{stat: p, ok: true}
			reused[i] = true
			f.markGoodBlob(fi.rel, fi.blob) // a cursor lists only files that decoded (for Problems)
			return
		}
		if r, ok := f.goodRow(fi.rel, fi.blob); ok && !known {
			// decoded already in this process under this blob id; a file
			// the cursor knew under another blob id changed, and is
			// returned as changed below
			results[i] = checked{stat: FileStat{Path: fi.rel, Blob: fi.blob}, row: &r, ok: true}
			return
		}
		st, statted := stats[fi.rel]
		if !statted {
			info, err := os.Lstat(filepath.Join(f.dataDir, filepath.FromSlash(fi.rel)))
			if err != nil {
				return // gone since git looked, or unreadable: the next Read tries again
			}
			st = entry{size: info.Size(), mtime: nanos(info.ModTime())}
		}
		c := &results[i]
		c.stat, c.row, c.ok, c.bad = f.check(fi.rel, st, p, known, prev.At, at)
		if c.ok {
			c.stat.Blob = fi.blob
			if c.row != nil {
				f.markGood(fi.rel, fi.blob, *c.row)
			}
		}
	}); err != nil {
		return nil, "", err
	}
	next := StatCursor{At: at, Files: make([]FileStat, 0, len(files))}
	var out []store.Row
	var bad []Problem
	changed := false
	for i, c := range results {
		if c.bad != nil {
			bad = append(bad, Problem{Path: files[i].rel, Reason: c.bad.Error(), Code: errs.CodeOf(c.bad), FieldMarkers: isFieldMarkers(c.bad)})
		}
		if !c.ok {
			continue
		}
		if c.row != nil {
			out = append(out, *c.row)
		}
		next.Files = append(next.Files, c.stat)
		if !reused[i] {
			p, known := seen[c.stat.Path]
			if !known || p != c.stat || racy(c.stat.MTime, prev.At) {
				changed = true
			}
		}
	}
	f.badMu.Lock()
	f.bad = bad
	f.badMu.Unlock()
	if cursor != "" && len(out) == 0 && !changed && len(next.Files) == len(prev.Files) {
		// B1-16 as in Read: nothing changed, the cursor is the answer and
		// the cache is left untouched.
		return nil, cursor, nil
	}
	c, err := next.Encode()
	if err != nil {
		return nil, "", err
	}
	return out, c, nil
}

// missingIndexed is Missing under an IndexView: the cursor's files that are
// neither clean in git nor present on disk.
func (f *Folder) missingIndexed(prev StatCursor, view IndexView) ([]string, error) {
	files, _, err := f.indexedFiles(view)
	if err != nil {
		return nil, err
	}
	there := make(map[string]bool, len(files))
	for _, fi := range files {
		there[fi.rel] = true
	}
	var out []string
	for _, p := range prev.Files {
		if !there[p.Path] {
			out = append(out, p.Path)
		}
	}
	return out, nil
}

// ReadTableMirrored implements the rowfs half of store.MirroredReader: the
// rows of table, each clean file's row taken from m when m holds it under
// the file's blob id, every other file read and decoded as readTables
// reads it (and kept in m when git calls it clean). ok is false when no
// IndexView can be trusted now.
func (f *Folder) ReadTableMirrored(ctx context.Context, table row.Table, m store.RowMirror) ([]store.Row, bool, error) {
	sub, ok := f.tableView(ctx, table)
	if !ok {
		return nil, false, nil
	}
	rows, _, err := f.readMirrored(ctx, sub, table, m, false)
	return rows, err == nil, err
}

// ReadAllMirrored is Read(ctx, "") through m (store.MirroredReader): every
// row of the folder, plus a cursor for the next Read. A cursor entry taken
// from m records the file's blob id only: the next Read reuses it while git
// reports the file clean under that blob id, and reads the file again
// otherwise (a row returned again is harmless to the cache).
func (f *Folder) ReadAllMirrored(ctx context.Context, m store.RowMirror) ([]store.Row, store.Cursor, bool, error) {
	view, ok := f.indexView(ctx)
	if !ok {
		return nil, "", false, nil
	}
	rows, cur, err := f.readMirrored(ctx, view, "", m, true)
	return rows, cur, err == nil, err
}

// readMirrored reads the row files of view (one table's, or all with
// table ""), each clean one from m when m holds it under its blob id. full
// records the unusable files for ReadProblems and builds the cursor.
func (f *Folder) readMirrored(ctx context.Context, view IndexView, table row.Table, m store.RowMirror, full bool) ([]store.Row, store.Cursor, error) {
	if err := checkChain(f.root.dir, f.rel); err != nil {
		return nil, "", err
	}
	files, stats, err := f.indexedFiles(view)
	if err != nil {
		return nil, "", err
	}
	held := map[string]store.Row{}
	if table != "" {
		held, err = m.Lookup(ctx, table, view.Clean)
	} else {
		for _, t := range tablesOf(files) {
			var got map[string]store.Row
			if got, err = m.Lookup(ctx, t, view.Clean); err != nil {
				break
			}
			maps.Copy(held, got)
		}
	}
	if err != nil {
		held = nil // best effort: the files are read instead
	}
	at := nanos(f.root.now())
	results := make([]checked, len(files))
	if err := parallel(ctx, len(files), func(i int) {
		fi := files[i]
		if r, ok := held[fi.rel]; ok && fi.blob != "" {
			results[i] = checked{stat: FileStat{Path: fi.rel, Blob: fi.blob}, row: &r, ok: true}
			return
		}
		if r, ok := f.goodRow(fi.rel, fi.blob); ok {
			results[i] = checked{stat: FileStat{Path: fi.rel, Blob: fi.blob}, row: &r, ok: true}
			return
		}
		st, statted := stats[fi.rel]
		if !statted {
			info, err := os.Lstat(filepath.Join(f.dataDir, filepath.FromSlash(fi.rel)))
			if err != nil {
				return
			}
			st = entry{size: info.Size(), mtime: nanos(info.ModTime())}
		}
		c := &results[i]
		c.stat, c.row, c.ok, c.bad = f.check(fi.rel, st, FileStat{}, false, 0, at)
		if c.ok {
			c.stat.Blob = fi.blob
		}
	}); err != nil {
		return nil, "", err
	}
	var out []store.Row
	var keep []store.MirrorEntry
	var bad []Problem
	next := StatCursor{At: at, Files: make([]FileStat, 0, len(files))}
	for i, c := range results {
		if c.bad != nil {
			bad = append(bad, Problem{Path: files[i].rel, Reason: c.bad.Error(), Code: errs.CodeOf(c.bad), FieldMarkers: isFieldMarkers(c.bad)})
		}
		if !c.ok || c.row == nil {
			continue
		}
		out = append(out, *c.row)
		next.Files = append(next.Files, c.stat)
		if files[i].blob == "" {
			continue
		}
		f.markGood(files[i].rel, files[i].blob, *c.row)
		if _, hit := held[files[i].rel]; !hit {
			keep = append(keep, store.MirrorEntry{Path: files[i].rel, Blob: files[i].blob, Row: *c.row})
		}
	}
	_ = m.Keep(ctx, keep) // best effort: a read-only cache keeps nothing, the next read reads the files again
	if !full {
		return out, "", nil
	}
	f.badMu.Lock()
	f.bad = bad
	f.badMu.Unlock()
	cur, err := next.Encode()
	if err != nil {
		return nil, "", err
	}
	return out, cur, nil
}

// tablesOf lists the table folders files are in.
func tablesOf(files []indexedFile) []row.Table {
	seen := map[row.Table]bool{}
	var out []row.Table
	for _, fi := range files {
		t, _, _ := strings.Cut(fi.rel, "/")
		if !seen[row.Table(t)] {
			seen[row.Table(t)] = true
			out = append(out, row.Table(t))
		}
	}
	return out
}

// goodRows remembers, for this process, each row file that decoded as a
// usable row and its row, under the blob id git gave its content: keyed by
// absolute path, so every Folder over the same data folder (a doctor run
// opens several) shares it. Same path and blob id means same bytes, so an
// entry is exact for as long as git reports that blob id.
var goodRows = struct {
	mu sync.Mutex
	by map[string]goodRow // absolute path → entry
}{by: map[string]goodRow{}}

// goodRow is one goodRows entry; has is false for a file known to decode
// well whose row this process never held (a stat cursor entry reused under
// the same blob id: a cursor lists only files that decoded).
type goodRow struct {
	blob string
	row  store.Row
	has  bool
}

// markGood records that rel decoded as r under blob id blob.
func (f *Folder) markGood(rel, blob string, r store.Row) {
	if blob == "" {
		return
	}
	goodRows.mu.Lock()
	goodRows.by[f.abs(rel)] = goodRow{blob: blob, row: r, has: true}
	goodRows.mu.Unlock()
}

// markGoodBlob records that rel decodes well under blob id blob, without
// its row (Problems need not read it again; a read still does).
func (f *Folder) markGoodBlob(rel, blob string) {
	if blob == "" {
		return
	}
	goodRows.mu.Lock()
	defer goodRows.mu.Unlock()
	if g, ok := goodRows.by[f.abs(rel)]; ok && g.blob == blob {
		return // keep the row a read left
	}
	goodRows.by[f.abs(rel)] = goodRow{blob: blob}
}

// goodRow is rel's remembered row when it decoded well under blob.
func (f *Folder) goodRow(rel, blob string) (store.Row, bool) {
	if blob == "" {
		return store.Row{}, false
	}
	goodRows.mu.Lock()
	g, ok := goodRows.by[f.abs(rel)]
	goodRows.mu.Unlock()
	return g.row, ok && g.has && g.blob == blob
}

// knownGood reports whether rel decodes as a usable row under blob.
func (f *Folder) knownGood(rel, blob string) bool {
	if blob == "" {
		return false
	}
	goodRows.mu.Lock()
	defer goodRows.mu.Unlock()
	g, ok := goodRows.by[f.abs(rel)]
	return ok && g.blob == blob
}

// abs is rel's absolute path.
func (f *Folder) abs(rel string) string { return filepath.Join(f.dataDir, filepath.FromSlash(rel)) }
