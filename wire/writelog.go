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

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
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
}

// WriteLog collects the row files one command wrote.
type WriteLog struct {
	mu    sync.Mutex
	files []Written
	seen  map[Written]bool
}

// writeLogKey carries the log on a context.
type writeLogKey struct{}

// WithWriteLog returns ctx with a new WriteLog and the log.
func WithWriteLog(ctx context.Context) (context.Context, *WriteLog) {
	l := &WriteLog{seen: map[Written]bool{}}
	return context.WithValue(ctx, writeLogKey{}, l), l
}

// writeLogOf is ctx's log, nil when none.
func writeLogOf(ctx context.Context) *WriteLog {
	l, _ := ctx.Value(writeLogKey{}).(*WriteLog)
	return l
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

// Files are the row files written, in write order.
func (l *WriteLog) Files() []Written {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Written(nil), l.files...)
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
			if f.File == "" {
				out = append(out, "lore: wrote a row in store "+f.Store)
				continue
			}
			out = append(out, "lore: wrote "+f.File+storeSuffix(f.Store))
		}
		return out
	}
	type group struct{ store, dir string }
	counts := map[group]int{}
	var order []group
	for _, f := range files {
		g := group{store: f.Store}
		if f.File != "" {
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
		if g.dir == "" {
			out = append(out, "lore: wrote "+strconv.Itoa(n)+" row(s) in store "+g.store)
			continue
		}
		out = append(out, "lore: wrote "+strconv.Itoa(n)+" row file(s) in "+g.dir+storeSuffix(g.store))
	}
	return out
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
