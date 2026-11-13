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
	// key is "table/id", matching NoteLiveWrite's records.
	key string
}

// WriteLog collects the row files one command wrote.
type WriteLog struct {
	mu    sync.Mutex
	files []Written
	seen  map[Written]bool
	// live maps "table/id" to the live ref a row of this command went to.
	live map[string]string
}

// writeLogKey carries the log on a context.
type writeLogKey struct{}

// WithWriteLog returns ctx with a new WriteLog and the log.
func WithWriteLog(ctx context.Context) (context.Context, *WriteLog) {
	l := &WriteLog{seen: map[Written]bool{}, live: map[string]string{}}
	return context.WithValue(ctx, writeLogKey{}, l), l
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
	}
	return out
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
	if len(files) <= WrittenShowAll {
		out := make([]string, 0, len(files))
		for _, f := range files {
			if f.Live != "" {
				out = append(out, liveWrittenLine(f))
				continue
			}
			if f.File == "" {
				out = append(out, "lore: wrote a row in store "+f.Store)
				continue
			}
			out = append(out, "lore: wrote "+f.File+storeSuffix(f.Store))
		}
		return out
	}
	type group struct{ store, dir, live string }
	counts := map[group]int{}
	var order []group
	for _, f := range files {
		g := group{store: f.Store, live: f.Live}
		if f.File != "" && f.Live == "" {
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
	return out
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
