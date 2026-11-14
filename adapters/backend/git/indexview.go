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
	dir := filepath.Join(b.work, filepath.FromSlash(b.folder.DataPath()))
	spec := "."
	if scope != "" {
		spec = scope // one table folder; paths stay relative to dir
	}
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
	changed, ok := b.worktreeChanges(ctx, scope, dir, spec)
	if !ok {
		return rowfs.IndexView{}, false
	}
	// Untracked and ignored row files are read too, as a walk of the folder
	// reads them: the whole-folder status lists them ("??", "!!"); a table
	// read asks ls-files -o (without --exclude-standard) for its folder.
	var other []byte
	if scope != "" {
		if other, err = b.git.run(ctx, call{dir: dir, args: []string{"ls-files", "--others", "-z", "--", spec}}); err != nil {
			return rowfs.IndexView{}, false
		}
	}
	for _, rel := range append(changed, splitZ(other)...) {
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

// worktreeChanges lists the files of the data folder (scope "") or of one
// table folder whose work tree copy differs from the index, deleted ones
// and untracked ones included, relative to dir. The whole folder is read
// from the status HandChanges reads too (repoStatus), so one command asks
// git once; one table folder from `git diff-files` and `git ls-files -o`
// on that folder alone.
func (b *Backend) worktreeChanges(ctx context.Context, scope, dir, spec string) ([]string, bool) {
	if scope != "" {
		out, err := b.git.run(ctx, call{dir: dir, args: []string{"diff-files", "--name-only", "--relative", "-z", "--", spec}})
		if err != nil {
			return nil, false
		}
		return splitZ(out), true
	}
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
	m := statusMemoFor(b.work + "\x00" + strings.Join(args, "\x00"))
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := b.indexFileStat(ctx)
	gen := rowfs.WriteGeneration()
	scope := store.ReadScopeOf(ctx)
	if m.ok && scope != nil && m.scope == scope && m.gen == gen && m.index == idx && idx != (indexStat{}) {
		return m.out, nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		m.ok = false
		return nil, err
	}
	if out, err = b.workRelative(ctx, out); err != nil {
		m.ok = false
		return nil, err
	}
	m.ok, m.gen, m.scope, m.index, m.out = true, gen, scope, idx, out
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
