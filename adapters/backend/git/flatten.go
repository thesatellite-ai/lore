package git

// flatten.go — one commit that moves a store's tree from the layout of an
// earlier 0.1.18 build to the plain store layout (plain files round, brief
// 1.2, 1.6, D-ad):
//
//	<folder>/.lore/data/<table>/<id>.json  →  <folder>/<table>/<id>.json
//	<folder>/.lore/data/_meta.json         →  <folder>/_meta.json (and _purged.json)
//	.lore/data/…                           →  _store/… (the store's root folder)
//	<folder>/.lore/lore-org.yaml           →  <folder>/lore-org.yaml
//	lore-store.yaml                        lore_store: 4 (the plain layout;
//	                                       an older lore refuses it), and
//	                                       org_folder only where that folder
//	                                       holds lore-org.yaml
//	README.md                              written when the store has none,
//	                                       or replaced when it is an earlier
//	                                       lore's text (rowfs.ReadmeOutdated)
//
// A store already in the first plain layout (lore_store: 3, the root
// folder's tables at the store's top) gets the same kind of commit, titled
// StoreDirSubject, moving only the root folder's files:
//
//	<table>/<id>.json                      →  _store/<table>/<id>.json
//	_meta.json, _purged.json               →  _store/_meta.json, _store/_purged.json
//	.lore/lock, .lore/state/…              lore's own files an earlier build
//	                                       committed: out of the index
//
// It runs under the store lock. Files are renamed on disk first (a
// person's uncommitted edit of a row moves with the row), then the index
// is changed in one `git update-index --index-info` holding only the
// committed content, so the commit carries the renames and nothing a
// person has not committed. A run cut short at any step is finished by the
// next one: every rule reads what is on disk and in the index, not what
// the last run meant to do. A row file found at both places (a teammate's
// older lore wrote one after the move) is merged with the row merger.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// FlattenSubject is the flatten commit's title for a store in the layout
// before the plain files round (rows under <folder>/.lore/data).
const FlattenSubject = "lore: store layout 0.1.18 — rows move from <folder>/.lore/data/ to <folder>/"

// StoreDirSubject is the layout commit's title for a store of the first
// plain layout (lore_store: 3): only the root folder's files move, from the
// store's top into _store/ (D-ad).
const StoreDirSubject = "lore: store layout — the store's own rows move to _store/"

// FlattenResult is what FlattenLayout did.
type FlattenResult struct {
	// Commit is the flatten commit ("" when the store was in the plain
	// layout already: nothing done).
	Commit string
	// Moved counts the files moved.
	Moved int
	// Folders are the folders whose rows moved ("." the root), sorted.
	Folders []string
	// Merged lists the files found at both places and merged.
	Merged []string
	// Kept lists the files found at both places that could not be merged:
	// the plain-layout file is kept, the other stays in the history.
	Kept []string
	// Untracked lists lore's own state files an earlier build committed
	// (an old worktree's .lore/lock): no longer tracked; a lock file is
	// removed (it holds nothing), anything else is left on disk.
	Untracked []string
	// StoreDir is true when the commit only moved the root folder's files
	// of a first-plain-layout store into _store/ (StoreDirSubject); false
	// for the move from <folder>/.lore/data (FlattenSubject).
	StoreDir bool
	// Change is what the commit changed, for the line lore prints
	// (rowfs.LayoutChange.Summary: README and marker included, never a
	// zero count).
	Change rowfs.LayoutChange
}

// NeedsFlatten reports whether the clone still needs the layout commit:
// its tree is in the old layout (rowfs.IsOldStoreLayout), its root
// folder's files still sit at the store's top (rowfs.FlatRootLeft), its
// marker names an earlier layout, or a flatten was cut short after the
// files moved (FlattenPending). A v0.1.17 store (marker 1) is left to
// `lore migrate run` first. Files only, no git.
func (b *Backend) NeedsFlatten() bool {
	if b.mode != modeClone || (b.marker.Version != 0 && b.marker.Version < rowfs.MarkerFirstStores) {
		return false
	}
	return NeedsFlattenAt(b.work)
}

// flattenPendingFile is written into the clone's .git/lore when a flatten
// starts and removed after its commit: a run cut short in between is
// finished by the next one even when the files have all moved already.
const flattenPendingFile = "flatten-pending"

// NeedsFlattenAt is NeedsFlatten for the clone at dir, before it is opened.
// Only a lore store (lore-store.yaml at its top) is moved: a clone of a
// repository that is not a store (a code repo listed under stores: by
// mistake, with its own .lore/data) is never a candidate, so the first-run
// step does not try to open it on every command.
func NeedsFlattenAt(dir string) bool {
	if isFile(filepath.Join(StateDirOf(dir), flattenPendingFile)) {
		return true
	}
	if !isFile(filepath.Join(dir, rowfs.MarkerFile)) {
		return false
	}
	if m, err := rowfs.ReadMarker(dir); err == nil && m.Version < rowfs.MarkerFirstStores {
		return false // a v0.1.17 store: `lore migrate run` first
	} else if err == nil && m.Version < rowfs.MarkerVersion && !m.ID.IsZero() {
		return true // the marker names an earlier layout: the commit sets the current one
	}
	if rowfs.FlatRootLeft(dir) {
		return true
	}
	old, err := rowfs.IsOldStoreLayout(dir)
	return err == nil && old
}

// flatMove is one file to move.
type flatMove struct {
	from, to string // root-relative slash paths
}

// FlattenLayout moves the clone's tree to the plain store layout in one
// commit (see the file comment). It does not push: the caller pushes per
// the store's push mode. The Backend's folder still points where it did
// at open: close it and open the store again after a flatten.
func (b *Backend) FlattenLayout(ctx context.Context) (FlattenResult, error) {
	var res FlattenResult
	if err := b.usable(ctx); err != nil {
		return res, err
	}
	if b.mode != modeClone {
		return res, nil
	}
	if b.readOnly {
		return res, store.ErrReadOnly
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = lock.Release() }() // the commit is made or not by then
	if !b.NeedsFlatten() {
		return res, nil
	}
	pending := filepath.Join(StateDirOf(b.work), flattenPendingFile)
	if err := os.MkdirAll(filepath.Dir(pending), cloneDirMode); err != nil {
		return res, errs.FileIO("create", filepath.Dir(pending), err)
	}
	if err := os.WriteFile(pending, []byte("a store layout move started here; the next lore command finishes it\n"), 0o600); err != nil {
		return res, errs.FileIO("write", pending, err)
	}
	moves, err := b.oldLayoutFiles(ctx)
	if err != nil {
		return res, err
	}
	res.StoreDir = b.marker.Version >= flatMarkerVersion && !slices.ContainsFunc(moves, func(m flatMove) bool { return rowfs.OldLayoutPath(m.from) })
	index, err := b.indexEntries(ctx)
	if err != nil {
		return res, err
	}
	var info bytes.Buffer
	folders := map[string]bool{}
	for _, m := range moves {
		line, merged, kept, merr := b.flattenOne(ctx, m, index)
		if merr != nil {
			return res, merr
		}
		info.WriteString(line)
		if merged {
			res.Merged = append(res.Merged, m.to)
		}
		if kept {
			res.Kept = append(res.Kept, m.to)
		}
		res.Moved++
		folders[folderOfOld(m.from)] = true
	}
	for p := range index {
		if loreBookkeeping(p) {
			// lore's own lock or journal committed by an earlier build: out
			// of the index (the file stays; it was never a row)
			info.WriteString("0 " + zeroOID + "\t" + p + "\n")
			res.Untracked = append(res.Untracked, p)
			if path.Base(p) == rowfs.LockFileName {
				_ = os.Remove(filepath.Join(b.work, filepath.FromSlash(p))) // a lock file holds nothing; the clone's lock is .git/lore/lock
			}
		}
	}
	sort.Strings(res.Untracked)
	if info.Len() > 0 {
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-index", "--index-info"}, stdin: info.Bytes()}); err != nil {
			return res, errs.Wrap(errs.Internal, "git: stage the moved row files", err)
		}
	}
	removeEmptyOldDirs(b.work, moves)
	markerPath := filepath.Join(b.work, rowfs.MarkerFile)
	markerBefore, _ := os.ReadFile(markerPath) // missing reads as empty: any written marker differs
	stage, err := b.fixMarker()
	if err != nil {
		return res, err
	}
	if markerAfter, rerr := os.ReadFile(markerPath); rerr == nil && !bytes.Equal(markerBefore, markerAfter) {
		res.Change.Marker = rowfs.MarkerVersion
	}
	hadReadme := rowfs.HasReadme(b.work)
	if wrote, rerr := rowfs.EnsureReadme(b.work); rerr != nil {
		return res, rerr
	} else if wrote {
		stage = append(stage, rowfs.ReadmeFile)
		res.Change.Readme = rowfs.ReadmeAdded
		if hadReadme {
			res.Change.Readme = rowfs.ReadmeRefreshed
		}
	}
	if len(stage) > 0 {
		if _, err := b.git.run(ctx, call{dir: b.work, args: append([]string{"add", "--"}, stage...)}); err != nil {
			return res, errs.Wrap(errs.Internal, "git: stage "+strings.Join(stage, ", "), err)
		}
	}
	for f := range folders {
		res.Folders = append(res.Folders, f)
	}
	sort.Strings(res.Folders)
	res.Change.Moved, res.Change.FromOld, res.Change.Folders, res.Change.Untracked = res.Moved, !res.StoreDir, res.Folders, len(res.Untracked)
	if _, derr := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--cached", "--quiet"}}); derr == nil {
		// nothing to commit: a run cut short after its commit, or a marker
		// and files that were current already
		_ = os.Remove(pending)
		return res, nil
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return res, err
	}
	msg := FlattenSubject + "\n\n" + res.Change.Summary() + "." +
		"\nThe store's own rows (its top folder's) go to " + rowfs.StoreDataDir + "/; every other folder is a project or the org folder." +
		"\nlore-store.yaml now says lore_store: " + strconv.Itoa(rowfs.MarkerVersion) + "; a lore older than this layout refuses the store and asks to upgrade."
	if res.StoreDir {
		msg = StoreDirSubject + "\n\n" + res.Change.Summary() + "." +
			"\nThe store's own rows (actors, machines, …) live in " + rowfs.StoreDataDir + "/, apart from the project folders and the org folder." +
			"\nlore-store.yaml now says lore_store: " + strconv.Itoa(rowfs.MarkerVersion) + "; a lore older than this layout refuses the store and asks to upgrade."
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "-m", b.cfg.withTrailers(msg)}, env: env}); err != nil {
		return res, errs.Wrap(errs.Internal, "git: commit the store layout move", err).
			WithHint("check the clone with git -C " + b.work + " status, then run the command again (lore finishes the move)")
	}
	if res.Commit, err = b.head(ctx); err != nil {
		return res, err
	}
	_ = os.Remove(pending) // a left file only means the next run checks again and finds nothing
	b.dropCaches()
	return res, nil
}

// Head is the clone's current commit ("" before the first one).
func (b *Backend) Head(ctx context.Context) (string, error) { return b.head(ctx) }

// dropCaches forgets what the backend read before the tree moved.
func (b *Backend) dropCaches() {
	b.fullMu.Lock()
	b.fullHead, b.fullRows = "", nil
	b.fullMu.Unlock()
}

// oldLayoutFiles lists every file to move: tracked ones (from the index)
// and untracked ones on disk (a carried new row), each once, sorted.
func (b *Backend) oldLayoutFiles(ctx context.Context) ([]flatMove, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: list the store's files", err)
	}
	seen := map[string]bool{}
	var list []flatMove
	for _, p := range splitZ(out) {
		to, ok := FlatPath(p)
		if !ok {
			to, ok = StoreDirPath(p)
		}
		if !ok || seen[p] {
			continue
		}
		seen[p] = true
		list = append(list, flatMove{from: p, to: to})
	}
	slices.SortFunc(list, func(a, c flatMove) int { return strings.Compare(a.from, c.from) })
	return list, nil
}

// flatMarkerVersion is the marker of the first plain layout (root folder
// at the store's top); StoreDirPath moves what it left.
const flatMarkerVersion = 3

// StoreDirPath is where a root folder file of the first plain layout
// (lore_store: 3) goes: <table>/<id>.json, _meta.json and _purged.json at
// the store's top → the same under _store/ (rowfs.StoreDataDir, D-ad). ok
// is false for any other path.
func StoreDirPath(p string) (string, bool) {
	if p == rowfs.MetaFileName || p == rowfs.PurgedFileName {
		return path.Join(rowfs.StoreDataDir, p), true
	}
	if _, _, ok := rowfs.ParseRowPath(p); ok {
		return path.Join(rowfs.StoreDataDir, p), true
	}
	return "", false
}

// FlatPath is where a file of the old store layout goes in the plain one:
// <folder>/.lore/data/<rest> → <folder>/<rest> (the root folder's
// .lore/data/<rest> → _store/<rest>), <folder>/.lore/lore-org.yaml →
// <folder>/lore-org.yaml. ok is false for any other path (lore's own
// .lore/lock or journal of an old worktree stays where it is).
func FlatPath(p string) (string, bool) {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s != rowfs.LoreDir {
			continue
		}
		rest := segs[i+1:]
		switch {
		case len(rest) >= 2 && rest[0] == rowfs.DataDir && i == 0:
			return path.Join(append([]string{rowfs.StoreDataDir}, rest[1:]...)...), true
		case len(rest) >= 2 && rest[0] == rowfs.DataDir:
			return path.Join(append(slices.Clone(segs[:i]), rest[1:]...)...), true
		case isOrgFileAt(rest):
			return path.Join(append(slices.Clone(segs[:i]), rest[0])...), true
		}
		return "", false
	}
	return "", false
}

// folderOfOld is the folder an old-layout path belongs to ("." the root,
// also for a root folder file of the first plain layout).
func folderOfOld(p string) string {
	if !strings.Contains(p, "/"+rowfs.LoreDir+"/") && !strings.HasPrefix(p, rowfs.LoreDir+"/") {
		return rowfs.FolderRoot
	}
	i := strings.Index(p, rowfs.LoreDir+"/")
	if i <= 0 {
		return rowfs.FolderRoot
	}
	return strings.TrimSuffix(p[:i], "/")
}

// indexEntry is one stage-0 index entry.
type indexEntry struct{ mode, oid string }

// indexEntries reads the index: path → mode and object id (stage 0 only).
func (b *Backend) indexEntries(ctx context.Context) (map[string]indexEntry, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-files", "-s", "-z"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the store's index", err)
	}
	m := map[string]indexEntry{}
	for _, e := range splitZ(out) {
		meta, p, ok := strings.Cut(e, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[2] != "0" {
			continue
		}
		m[p] = indexEntry{mode: f[0], oid: f[1]}
	}
	return m, nil
}

// flattenOne moves one file on disk and returns its index-info lines.
// merged reports a file found at both places and merged; kept one that
// could not be merged (the plain-layout copy stays).
func (b *Backend) flattenOne(ctx context.Context, m flatMove, index map[string]indexEntry) (line string, merged, kept bool, err error) {
	from := filepath.Join(b.work, filepath.FromSlash(m.from))
	to := filepath.Join(b.work, filepath.FromSlash(m.to))
	oldIdx, oldTracked := index[m.from]
	newIdx, newTracked := index[m.to]
	_, toErr := os.Lstat(to)
	toOnDisk := toErr == nil
	fromBytes, fromErr := os.ReadFile(from)
	fromOnDisk := fromErr == nil
	switch {
	case fromOnDisk && !toOnDisk:
		if err = os.MkdirAll(filepath.Dir(to), cloneDirMode); err != nil {
			return "", false, false, errs.FileIO("create", filepath.Dir(to), err)
		}
		if err = os.Rename(from, to); err != nil {
			return "", false, false, errs.FileIO("move", from, err)
		}
	case fromOnDisk && toOnDisk:
		toBytes, rerr := os.ReadFile(to)
		if rerr != nil {
			return "", false, false, errs.FileIO("read", to, rerr)
		}
		if !bytes.Equal(fromBytes, toBytes) {
			out, ok := b.mergeBoth(m.to, toBytes, fromBytes)
			merged, kept = ok, !ok
			if ok {
				if err = os.WriteFile(to, out, 0o600); err != nil {
					return "", false, false, errs.FileIO("write", to, err)
				}
			}
		}
		if err = os.Remove(from); err != nil {
			return "", false, false, errs.FileIO("remove", from, err)
		}
	}
	var sb strings.Builder
	if oldTracked {
		sb.WriteString("0 " + zeroOID + "\t" + m.from + "\n")
		switch {
		case !newTracked:
			sb.WriteString(oldIdx.mode + " " + oldIdx.oid + "\t" + m.to + "\n")
		case merged:
			oid, herr := b.git.run(ctx, call{dir: b.work, args: []string{"hash-object", "-w", "--", m.to}})
			if herr != nil {
				return "", false, false, errs.Wrap(errs.Internal, "git: store the merged "+m.to, herr)
			}
			sb.WriteString(newIdx.mode + " " + trimmed(oid) + "\t" + m.to + "\n")
		}
	}
	return sb.String(), merged, kept, nil
}

// mergeBoth merges a file found at both places: a folder file with the
// folder-file merge, a row with the row merger (base unknown). ok false
// when it cannot be merged.
func (b *Backend) mergeBoth(p string, plain, old []byte) ([]byte, bool) {
	if name := path.Base(p); name == rowfs.MetaFileName || name == rowfs.PurgedFileName {
		out, err := rowfs.MergeFolderFile(p, nil, plain, old)
		return out, err == nil
	}
	if b.cfg.merger == nil || !strings.HasSuffix(p, ".json") {
		return nil, false
	}
	out, conflict, err := b.cfg.merger.Merge(nil, plain, old)
	return out, err == nil && !conflict
}

// removeEmptyOldDirs removes the old .lore/data folders the move emptied,
// and each .lore folder left empty; a folder still holding anything (an
// old worktree's lock) stays.
func removeEmptyOldDirs(top string, moves []flatMove) {
	dirs := map[string]bool{}
	for _, m := range moves {
		for d := path.Dir(m.from); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
			if path.Base(d) == rowfs.LoreDir {
				break
			}
		}
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) }) // deepest first
	for _, d := range list {
		_ = os.Remove(filepath.Join(top, filepath.FromSlash(d))) // only an empty folder goes
	}
}

// fixMarker rewrites lore-store.yaml for the plain layout: lore_store at
// rowfs.MarkerVersion, org_folder only when that folder holds lore-org.yaml.
// It returns the paths to stage.
func (b *Backend) fixMarker() ([]string, error) {
	m, err := rowfs.ReadMarker(b.work)
	if errors.Is(err, store.ErrNotAStore) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if m.OrgFolder != "" {
		p := orgConfigName
		if m.OrgFolder != rowfs.FolderRoot {
			p = path.Join(m.OrgFolder, orgConfigName)
		}
		if _, serr := os.Stat(filepath.Join(b.work, filepath.FromSlash(p))); errors.Is(serr, fs.ErrNotExist) {
			m.OrgFolder = ""
		}
	}
	if m.ID.IsZero() {
		return nil, nil // a v0.1.17 marker: `lore migrate run` gives it an id first
	}
	if err := rowfs.WriteMarker(b.work, m); err != nil {
		return nil, err
	}
	b.marker = m
	b.marker.Version = rowfs.MarkerVersion
	return []string{rowfs.MarkerFile}, nil
}
