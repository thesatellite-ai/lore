package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// indexView is the code repo's rowfs.IndexView of the data folder (perf3,
// D-110): git's index lists every tracked row file with its blob id, and
// `git diff-files` and `git ls-files -o` name the few files that differ
// from it, so a warm read knows the whole folder from two small reads of
// .git/index instead of lore stat-ing every row file. Git checks the work
// tree against the index's own stat data (racy entries by content), which
// is the same test lore's stat cursor makes, done once in C with threads.
// scope "" is the whole data folder, else one table folder (IndexSource).
//
// ok is false, and the read walks the folder as before, when git fails or
// any entry is unmerged (a merge in progress: those files are read as the
// folder has them, conflict markers and all).
func (b *Backend) indexView(ctx context.Context, scope string) (rowfs.IndexView, bool) {
	if scope != "" {
		// One table: the slice of the whole folder's view (devco:D-418).
		// Asking git per table (ls-files --stage, diff-files, ls-files -o)
		// cost three git processes per table on every read; the whole view
		// is two (ls-files --stage and the status repoStatus remembers),
		// once per command.
		whole, ok := b.wholeIndexView(ctx)
		if !ok {
			return rowfs.IndexView{}, false
		}
		return viewUnder(whole, scope+"/"), true
	}
	dir := filepath.Join(b.work, filepath.FromSlash(b.folder.DataPath()))
	spec := "."
	staged, err := b.git.run(ctx, call{dir: dir, args: []string{"ls-files", "--stage", "-z", "--", spec}})
	if err != nil {
		return rowfs.IndexView{}, false
	}
	view := rowfs.IndexView{Clean: map[string]string{}}
	for _, rec := range splitZ(staged) {
		meta, rel, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta) // <mode> <blob> <stage>
		if len(fields) != 3 {
			return rowfs.IndexView{}, false
		}
		if fields[2] != "0" {
			return rowfs.IndexView{}, false // unmerged
		}
		mode := fields[0]
		if mode != "100644" && mode != "100755" {
			view.Check = append(view.Check, rel) // a link or a submodule: lstat says what it is
			continue
		}
		view.Clean[rel] = fields[1]
	}
	changed, ok := b.worktreeChanges(ctx)
	if !ok {
		return rowfs.IndexView{}, false
	}
	// Untracked and ignored row files are read too, as a walk of the folder
	// reads them: the whole-folder status lists them ("??", "!!").
	for _, rel := range changed {
		if rel == "" {
			continue
		}
		delete(view.Clean, rel)
		view.Check = append(view.Check, rel)
	}
	return view, true
}

// ReadTableMirrored implements store.MirroredReader in a code repo (perf3):
// a warm list of a table takes each row git reports unchanged from m. A
// store clone answers ok false (it reads commits, rowfs.ReadTableMirrored
// needs the repo layout's IndexView).
func (b *Backend) ReadTableMirrored(ctx context.Context, table row.Table, m store.RowMirror) ([]store.Row, bool, error) {
	if b.mode != modeRepo {
		return nil, false, nil
	}
	if err := b.usable(ctx); err != nil {
		return nil, false, err
	}
	return b.folder.ReadTableMirrored(ctx, table, m)
}

// worktreeChanges lists the files of the data folder whose work tree copy
// differs from the index, deleted, untracked and ignored ones included,
// relative to the data folder. It reads the status HandChanges reads too
// (repoStatus), so one command asks git once.
func (b *Backend) worktreeChanges(ctx context.Context) ([]string, bool) {
	out, err := b.repoStatus(ctx)
	if err != nil {
		return nil, false
	}
	prefix := b.folder.DataPath() + "/"
	var rels []string
	for _, f := range splitZ(out) {
		if len(f) < len("XY ")+1 {
			continue
		}
		status, p := f[:2], f[3:]
		if status[1] == ' ' {
			continue // staged only: the work tree holds what the index holds
		}
		if rel, ok := strings.CutPrefix(p, prefix); ok {
			rels = append(rels, rel)
		}
	}
	return rels, true
}

// viewUnder is the part of v under prefix (a table folder + "/"), as the
// view of that folder alone; v itself is never changed (it is shared).
func viewUnder(v rowfs.IndexView, prefix string) rowfs.IndexView {
	out := rowfs.IndexView{Clean: map[string]string{}}
	for rel, blob := range v.Clean {
		if strings.HasPrefix(rel, prefix) {
			out.Clean[rel] = blob
		}
	}
	for _, rel := range v.Check {
		if strings.HasPrefix(rel, prefix) {
			out.Check = append(out.Check, rel)
		}
	}
	return out
}

// viewMemo is the whole data folder's IndexView remembered for one
// command (the same validity as statusMemo): the per-table reads of one
// command slice it instead of asking git again.
type viewMemo struct {
	mu    sync.Mutex
	ok    bool
	gen   uint64
	scope *store.ReadScope
	index indexStat
	view  rowfs.IndexView
}

// viewMemos are the process's viewMemo values by work tree.
var viewMemos = struct {
	mu sync.Mutex
	by map[string]*viewMemo
}{by: map[string]*viewMemo{}}

// wholeIndexView is indexView(ctx, "") remembered while the index file
// did not change and the command (ReadScope) is the same, carried over the
// row files this process wrote since (rowfs.CarryView).
func (b *Backend) wholeIndexView(ctx context.Context) (rowfs.IndexView, bool) {
	viewMemos.mu.Lock()
	m, ok := viewMemos.by[b.work]
	if !ok {
		m = &viewMemo{}
		viewMemos.by[b.work] = m
	}
	viewMemos.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := b.indexFileStat(ctx)
	gen := rowfs.WriteGeneration()
	scope := store.ReadScopeOf(ctx)
	if m.ok && scope != nil && m.scope == scope && m.index == idx && idx != (indexStat{}) {
		if m.gen == gen {
			return m.view, true
		}
		// only lore's own row files changed since (the index file did
		// not): the view carried over them answers without a second git
		// status, e.g. a write's render refresh (devco:D-945, bug hunt H-203)
		dataDir := filepath.Join(b.work, filepath.FromSlash(b.folder.DataPath()))
		if view, now, ok := rowfs.CarryView(m.view, m.gen, dataDir); ok {
			m.view, m.gen = view, now
			return view, true
		}
	}
	view, vok := b.indexView(ctx, "")
	m.ok, m.gen, m.scope, m.index, m.view = vok, gen, scope, b.indexFileStat(ctx), view // the index as status left it
	return view, vok
}

// statusMemo is one remembered `git status` of the code repo's data folder
// (repoStatus).
type statusMemo struct {
	mu    sync.Mutex
	ok    bool
	gen   uint64           // rowfs.WriteGeneration when it ran
	scope *store.ReadScope // the command it ran in
	index indexStat        // .git/index when it ran
	out   []byte
}

// statusMemos are the process's statusMemo values by repo and question:
// one doctor run opens several Backends over the same repo, and they share
// the answer.
var statusMemos = struct {
	mu sync.Mutex
	by map[string]*statusMemo
}{by: map[string]*statusMemo{}}

// statusMemoFor is key's statusMemo, made on first use.
func statusMemoFor(key string) *statusMemo {
	statusMemos.mu.Lock()
	defer statusMemos.mu.Unlock()
	m, ok := statusMemos.by[key]
	if !ok {
		m = &statusMemo{}
		statusMemos.by[key] = m
	}
	return m
}

// indexStat is the size and modification time of the repo's index file:
// any git command that changes the index (lore's own git add) changes it.
type indexStat struct {
	size  int64
	mtime int64
}

// repoStatus is `git status --porcelain -z --untracked-files=all` of the
// code repo's data folder, remembered for the rest of the command (a
// store.ReadScope) while no file was written through rowfs and the index
// file did not change: the
// IndexView of a read and the pick-up of hand edits (HandChanges) ask the
// same question in one command, and git answers it by checking the stat
// of every tracked row file (perf3).
func (b *Backend) repoStatus(ctx context.Context) ([]byte, error) {
	// --ignored: the read's IndexView takes ignored row files from this
	// one walk too (lines "!! path"), instead of a second walk of the data
	// folder (ls-files --others) on every read (D-110: each walk lstats
	// every row file, slow on macOS); HandChanges skips "!!" lines.
	args := append([]string{"-c", "core.quotePath=false", "status", "--porcelain", "-z", "--untracked-files=all", "--ignored", "--no-renames", "--"}, b.root.Layout().Pathspecs(b.folder.Name())...)
	key := b.work + "\x00" + strings.Join(args, "\x00")
	m := statusMemoFor(key)
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := b.indexFileStat(ctx)
	gen := rowfs.WriteGeneration()
	scope := store.ReadScopeOf(ctx)
	if m.ok && scope != nil && m.scope == scope && m.gen == gen && m.index == idx && idx != (indexStat{}) {
		return m.out, nil
	}
	// across commands: kept while the data folder's files did not change
	// (statusstamp.go, bug hunt H-215)
	out, err := statusKept(key, func() stampInput { return b.repoStatusStamp(ctx) }, func() ([]byte, error) {
		return b.git.run(ctx, call{dir: b.work, args: args})
	})
	if err != nil {
		m.ok = false
		return nil, err
	}
	if out, err = b.workRelative(ctx, out); err != nil {
		m.ok = false
		return nil, err
	}
	// the index as git left it: status refreshes the index's stat data and
	// may rewrite the file, which is no change of content (devco:D-418:
	// keyed on the stat from before, the next read of the same command
	// always missed and ran status again)
	m.ok, m.gen, m.scope, m.index, m.out = true, gen, scope, b.indexFileStat(ctx), out
	return out, nil
}

// workRelative rewrites the porcelain -z status out so each path is
// relative to b.work, as every caller reads it (IndexView, HandChanges):
// git prints porcelain paths relative to the repo's top, and a project in
// a sub-folder of the repo (lore init in web/) works from that folder, so
// its "web/.lore/data/…" lines matched nothing and a read of its rows came
// back empty. A line outside b.work is dropped (the pathspecs already keep
// to it). At the repo's top it returns out as is.
func (b *Backend) workRelative(ctx context.Context, out []byte) ([]byte, error) {
	b.workPrefixOnce.Do(func() {
		pre, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--show-prefix"}})
		b.workPrefix, b.workPrefixErr = trimmed(pre), err
	})
	if b.workPrefixErr != nil {
		return nil, b.workPrefixErr
	}
	if b.workPrefix == "" {
		return out, nil
	}
	var rel []byte
	for _, f := range splitZ(out) {
		if len(f) < len("XY ")+1 {
			continue
		}
		p, ok := strings.CutPrefix(f[3:], b.workPrefix)
		if !ok {
			continue
		}
		rel = append(rel, f[:3]+p...)
		rel = append(rel, 0)
	}
	return rel, nil
}

// indexFileStat is the stat of the repo's index file (zero when unknown:
// the memo is not used then).
func (b *Backend) indexFileStat(ctx context.Context) indexStat {
	b.indexPathOnce.Do(func() {
		out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--path-format=absolute", "--git-path", "index"}})
		if err == nil {
			b.indexPath = trimmed(out)
		}
	})
	if b.indexPath == "" {
		return indexStat{}
	}
	info, err := os.Stat(b.indexPath)
	if err != nil {
		return indexStat{}
	}
	return indexStat{size: info.Size(), mtime: info.ModTime().UnixNano()}
}

// ReadAllMirrored implements store.MirroredReader: a full read of a code
// repo's data folder through m (store clones answer ok false).
func (b *Backend) ReadAllMirrored(ctx context.Context, m store.RowMirror) ([]store.Row, store.Cursor, bool, error) {
	if b.mode != modeRepo {
		return nil, "", false, nil
	}
	if err := b.usable(ctx); err != nil {
		return nil, "", false, err
	}
	return b.folder.ReadAllMirrored(ctx, m)
}

// readMirrored is a full read (cursor "") through the row mirror the
// caller handed down for this backend (store.WithRowMirror): one table's
// rows under a table hint (store.WithTables), else every row and a cursor.
// ok is false without a mirror or an IndexView.
func (b *Backend) readMirrored(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, bool, error) {
	m := store.RowMirrorFor(ctx, b)
	if m == nil || cursor != "" {
		return nil, "", false, nil
	}
	tables := store.TablesOf(ctx)
	if len(tables) == 0 {
		return b.folder.ReadAllMirrored(ctx, m)
	}
	var out []store.Row
	for _, t := range tables {
		rows, ok, err := b.folder.ReadTableMirrored(ctx, t, m)
		if err != nil || !ok {
			return nil, "", ok, err
		}
		out = append(out, rows...)
	}
	return out, "", true, nil
}
