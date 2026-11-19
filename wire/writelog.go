package wire

// writelog.go — every write names the file it changed (plain files sweep
// 3.4, 2026-11-12). The host puts a WriteLog on a writing command's
// context; every audited row write (routedAudit.Record, the one place all
// writes pass) adds the store and the row file it went to; after the
// command the host prints them (wire/host writeloc.go).
//
//	host Around (not a read) ─▶ WithWriteLog ─▶ op runs ─▶ engine writes
//	   ─▶ routedAudit.Record ─▶ log.add(store, row file)
//	   ─▶ "lore: wrote <file>" (a few) / "lore: wrote N row files in <dir>"
//	liveBackend (wire/host) ─▶ NoteLiveWrite: the row went to the local
//	   live ref, not its file ─▶ "lore: wrote <table>/<id> on this clone's
//	   refs/lore/live (not pushed yet; <file> unchanged)" (C-5)

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Written is one row file a command wrote.
type Written struct {
	// Store is the attached store's name ("repo" for the repo's own rows).
	Store string
	// File is the row file's absolute path ("" when the store keeps no
	// file per row, or its backend cannot say).
	File string
	// Live is the local live ref the row was written to instead of File
	// ("" when the write changed File): File is then left as it was (C-5:
	// "wrote <file>" for a file the write did not touch sent the person
	// to commit an unchanged file).
	Live string
	// Layer is the code branch whose notes (a branch layer) the row went
	// to instead of File, with the folder ("feat/x notes of app"); "" when
	// none (BRANCH_LAYERS 7).
	Layer string
	// key is "table/id", matching NoteLiveWrite's records.
	key string
	// extra marks a file a commit of the command holds beside its rows (a
	// folder's _meta.json): always named on its own line, never counted
	// as a row file (devco D-691).
	extra bool
}

// WriteLog collects the row files one command wrote.
type WriteLog struct {
	mu    sync.Mutex
	files []Written
	seen  map[Written]bool
	// live maps "table/id" to the live ref a row of this command went to.
	live map[string]string
	// layer maps "table/id" to the branch layer a row went to.
	layer map[string]layerWrite
	// declared lists folders a first layer declared branch_layers in.
	declared []string
	// committed are the files this command's commits really hold, by the
	// real path of the checkout they were made in, in commit order
	// (store.NoteCommitted, devco D-691); tops lists those checkouts.
	committed map[string][]string
	tops      []string
	shownTop  map[string]string // real path → the checkout as its backend named it
}

// layerWrite is one row written to a branch layer.
type layerWrite struct{ branch, folder string }

// writeLogKey carries the log on a context.
type writeLogKey struct{}

// WithWriteLog returns ctx with a new WriteLog and the log.
func WithWriteLog(ctx context.Context) (context.Context, *WriteLog) {
	l := &WriteLog{seen: map[Written]bool{}, live: map[string]string{}, layer: map[string]layerWrite{}, committed: map[string][]string{}, shownTop: map[string]string{}}
	ctx = store.WithCommitted(ctx, l.noteCommitted)
	return context.WithValue(ctx, writeLogKey{}, l), l
}

// noteCommitted records the files one commit made in top holds.
func (l *WriteLog) noteCommitted(top string, paths []string) {
	real := realPath(top)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.committed[real]; !ok {
		l.tops = append(l.tops, real)
		l.shownTop[real] = top
	}
	for _, p := range paths {
		if !slices.Contains(l.committed[real], p) {
			l.committed[real] = append(l.committed[real], p)
		}
	}
}

// realPath is p with its symlinks resolved (its folder's when p itself is
// gone), else p cleaned: a row file and the checkout of its commit are
// compared by their real paths (a symlinked temp or home folder).
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return filepath.Clean(p)
}

// committedRel is file's slash path in the checkout of a commit of this
// command, and that checkout ("" when no commit of this command was made
// in a checkout holding file).
func (l *WriteLog) committedRel(file string) (top, rel string) {
	real := realPath(file)
	for _, t := range l.tops {
		if r, err := filepath.Rel(t, real); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return t, filepath.ToSlash(r)
		}
	}
	return "", ""
}

// byCommits is files (each where it is now) as the commits of this
// command hold them (devco D-691): a row file written to a checkout this
// command committed in is named only when its commit holds it (a row
// rewritten with the same bytes is not), and every other file such a
// commit holds (a folder's new _meta.json) is named after the rows, under
// the store of a row beside it: a file beside the rows (a folder's
// _meta.json), a row another write path of the command committed, or one a
// move took away ("removed"). Rows written to a live ref or a branch
// layer, and stores that commit nothing, are kept as written.
func (l *WriteLog) byCommits(files []Written) []Written {
	if len(l.tops) == 0 {
		return files
	}
	named := map[string]map[string]bool{} // top → rel paths already named
	storeOf := map[string]string{}        // top → the store of a row in it
	out := files[:0:0]
	for _, w := range files {
		if w.File == "" || w.Live != "" || w.Layer != "" {
			out = append(out, w)
			continue
		}
		top, rel := l.committedRel(w.File)
		if top == "" {
			out = append(out, w)
			continue
		}
		if !slices.Contains(l.committed[top], rel) || named[top][rel] {
			continue // unchanged: not in any commit of this command (or named already)
		}
		if named[top] == nil {
			named[top] = map[string]bool{}
		}
		named[top][rel] = true
		if _, ok := storeOf[top]; !ok {
			storeOf[top] = w.Store
		}
		out = append(out, w)
	}
	for _, top := range l.tops {
		st, ok := storeOf[top]
		if !ok {
			continue // a commit of a checkout no row of this log names (not this log's to report)
		}
		for _, rel := range l.committed[top] {
			if named[top][rel] {
				continue
			}
			out = append(out, Written{Store: st, File: filepath.Join(l.shownTop[top], filepath.FromSlash(rel)), extra: true})
		}
	}
	return out
}

// writeLogOf is ctx's log, nil when none.
func writeLogOf(ctx context.Context) *WriteLog {
	l, _ := ctx.Value(writeLogKey{}).(*WriteLog)
	return l
}

// NoteLiveWrite records that the row table/id of this command was written
// to the local live ref ref rather than to its row file. The live backend
// (wire/host) calls it once the local ref holds the row; WrittenLines then
// names the ref, not the file. A context without a log ignores it.
func NoteLiveWrite(ctx context.Context, table row.Table, id string, ref string) {
	l := writeLogOf(ctx)
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.live[string(table)+"/"+id] = ref
}

// NoteLayerWrite records that the row table/id went to the branch layer
// of code branch branch in store folder folder (wire branchlayer.go).
func NoteLayerWrite(ctx context.Context, table row.Table, id, branch, folder string) {
	l := writeLogOf(ctx)
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.layer[string(table)+"/"+id] = layerWrite{branch: branch, folder: folder}
}

// NoteLayerDeclared records that a first layer declared the format
// feature branch_layers in folder (one commit on the store branch).
func NoteLayerDeclared(ctx context.Context, folder string) {
	l := writeLogOf(ctx)
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !slices.Contains(l.declared, folder) {
		l.declared = append(l.declared, folder)
	}
}

// Declared are the folders whose first branch layer this command made.
func (l *WriteLog) Declared() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.declared...)
}

// add records w once.
func (l *WriteLog) add(w Written) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen[w] {
		return
	}
	l.seen[w] = true
	l.files = append(l.files, w)
}

// Files are the row files written, in write order, each where it is now:
// a row the command wrote into a store's old layout and that the same
// command then moved (the layout commit after a migration) is named at
// its new place, never at a path that no longer exists (currentRowFile).
func (l *WriteLog) Files() []Written {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]Written(nil), l.files...)
	for i := range out {
		out[i].File = currentRowFile(out[i].File)
		out[i].Live = l.live[out[i].key]
		if lw, ok := l.layer[out[i].key]; ok {
			out[i].Layer = lw.branch + " notes of " + lw.folder
			out[i].Live = ""
		}
	}
	return l.byCommits(out)
}

// oldDataSeg is the folder of the layout before plain files inside a
// store clone: <clone>/<folder>/.lore/data/<table>/<id>.json, or
// <clone>/.lore/data/… for the store's own rows.
const oldDataSeg = string(filepath.Separator) + rowfs.LoreDir + string(filepath.Separator) + rowfs.DataDir + string(filepath.Separator)

// currentRowFile is file, or where a layout move put it when file is gone:
// <folder>/.lore/data/<t>/<id>.json → <folder>/<t>/<id>.json (the layout
// commit), → <clone>/_store/<t>/<id>.json (the store's own rows, and
// 0.1.17's per-org personal notes). A file at neither place is left as
// it was written.
func currentRowFile(file string) string {
	i := strings.LastIndex(file, oldDataSeg)
	if file == "" || i < 0 || fileExists(file) {
		return file
	}
	before, rest := file[:i], file[i+len(oldDataSeg):]
	for _, c := range []string{
		filepath.Join(before, rest),
		filepath.Join(before, rowfs.StoreDataDir, rest),
		filepath.Join(filepath.Dir(before), rowfs.StoreDataDir, rest),
	} {
		if fileExists(c) {
			return c
		}
	}
	return file
}

// WrittenShowAll is how many files are named one by one; past it the
// files are counted per folder.
const WrittenShowAll = 3

// WrittenLines are the lines a command prints for what it wrote: each
// file when there are at most WrittenShowAll, else one line per folder
// with the count.
func WrittenLines(files []Written) []string {
	if len(files) == 0 {
		return nil
	}
	rows := 0 // files beside the rows (extra) never switch to the counted form
	for _, f := range files {
		if !f.extra {
			rows++
		}
	}
	if rows <= WrittenShowAll {
		out := make([]string, 0, len(files))
		for _, f := range files {
			if f.Layer != "" {
				out = append(out, layerWrittenLine(f))
				continue
			}
			if f.Live != "" {
				out = append(out, liveWrittenLine(f))
				continue
			}
			if f.File == "" {
				out = append(out, "lore: wrote a row in store "+f.Store)
				continue
			}
			if f.extra {
				out = append(out, extraLine(f))
				continue
			}
			out = append(out, "lore: wrote "+f.File+storeSuffix(f.Store))
		}
		return out
	}
	type group struct{ store, dir, live, layer string }
	counts := map[group]int{}
	var order []group
	var extras []string // files beside the rows, each named (devco D-691)
	for _, f := range files {
		if f.extra {
			extras = append(extras, extraLine(f))
			continue
		}
		g := group{store: f.Store, live: f.Live, layer: f.Layer}
		if f.File != "" && f.Live == "" && f.Layer == "" {
			g.dir = filepath.Dir(filepath.Dir(f.File)) // <folder data>/<table>/<id>.json → the folder's data
		}
		if counts[g] == 0 {
			order = append(order, g)
		}
		counts[g]++
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].dir < order[j].dir })
	out := make([]string, 0, len(order))
	for _, g := range order {
		n := counts[g]
		if g.layer != "" {
			out = append(out, "lore: wrote "+strconv.Itoa(n)+" row(s) to the "+g.layer+storeSuffix(g.store)+" (not shared with the default branch; lore branch land when the branch merges)")
			continue
		}
		if g.live != "" {
			out = append(out, "lore: wrote "+strconv.Itoa(n)+" live row(s) on this clone's "+g.live+storeSuffix(g.store)+" (not pushed yet; their files are unchanged)")
			continue
		}
		if g.dir == "" {
			out = append(out, "lore: wrote "+strconv.Itoa(n)+" row(s) in store "+g.store)
			continue
		}
		out = append(out, "lore: wrote "+strconv.Itoa(n)+" row file(s) in "+g.dir+storeSuffix(g.store))
	}
	return append(out, extras...)
}

// extraLine names one more file a commit of the command holds: "removed"
// when the commit took it away (a moved row's old place), else "wrote".
func extraLine(f Written) string {
	verb := "lore: wrote "
	if !fileExists(f.File) {
		verb = "lore: removed "
	}
	return verb + f.File + storeSuffix(f.Store)
}

// layerWrittenLine is the line of one row written to a branch layer.
func layerWrittenLine(f Written) string {
	return "lore: wrote " + f.key + " to the " + f.Layer + storeSuffix(f.Store) + " (not shared with the default branch; lore branch land when the branch merges)"
}

// liveWrittenLine is the line of one row written to the local live ref:
// where it went, that it is not pushed yet, and that its file (when it
// has one) did not change.
func liveWrittenLine(f Written) string {
	line := "lore: wrote " + f.key + " on this clone's " + f.Live + storeSuffix(f.Store) + " (a live row, not pushed yet"
	if f.File != "" {
		line += "; " + f.File + " is unchanged"
	}
	return line + ")"
}

// storeSuffix names the store after a path ("" for the repo's own rows,
// whose path says it).
func storeSuffix(s string) string {
	if s == "" || s == string(store.SourceRepo) {
		return ""
	}
	return " (store " + s + ")"
}

// rowFiler is a backend that can say where a row's file lives.
type rowFiler interface {
	RowFile(table row.Table, id ids.AnyID) (dir, rel string)
}

// dataPlace is a backend with a store folder and its data folder in it.
type dataPlace interface {
	Dir() string
	DataPath() string
}

// rowFileIn is the absolute row file of id in b ("" when b keeps none).
func rowFileIn(b store.Backend, table row.Table, id ids.AnyID) string {
	if b == nil {
		return ""
	}
	f, ok := concrete(b).(rowFiler)
	if !ok {
		// a folder store: its data folder (rowfs's layout) and the row path
		if p, pok := concrete(b).(dataPlace); pok && p.Dir() != "" {
			return filepath.Join(p.Dir(), filepath.FromSlash(p.DataPath()), filepath.FromSlash(rowfs.RowPath(table, id)))
		}
		return ""
	}
	dir, rel := f.RowFile(table, id)
	if dir == "" || rel == "" {
		return ""
	}
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// rowFileOf is the row file of id in the attached store name ("" when
// unknown): the write log's paths.
func (st *state) rowFileOf(name store.Source, table row.Table, id ids.AnyID) string {
	return rowFileIn(st.byName[name], table, id)
}

// placeLabelOf is the attached store name as every write and read line
// names it: "<store>/<folder>" for a store attached at a folder (the label
// search, the lists, sql's place column and the "committed locally to
// store …" note print), else the name itself. Before, the "lore: wrote …
// (store acme)" line named a linked repo's folder store by its bare
// attached name next to "committed locally to store acme/web" (devco
// D-600).
func (st *state) placeLabelOf(name store.Source) string {
	if name == "" || name == store.SourceRepo || strings.Contains(string(name), targetSep) {
		return string(name)
	}
	if f := strings.Trim(st.folders[name], "/"); f != "" && f != rowfs.FolderRoot {
		return string(name) + targetSep + f
	}
	return string(name)
}
