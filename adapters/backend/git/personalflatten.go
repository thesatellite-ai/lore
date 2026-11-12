package git

// personalflatten.go — a git personal store written by lore 0.1.17, moved
// to what this lore reads (upgrade journey B4-1). lore 0.1.17 filed `--to
// me` notes by place, one folder per org plus everywhere/:
//
//	<org>/.lore/data/<table>/<id>.json      a note written in a repo of <org>
//	everywhere/.lore/data/<table>/<id>.json  a note written anywhere else
//
// (after the layout commit, the same files sit at <org>/<table>/… and
// everywhere/<table>/…). This lore reads a git personal store's root
// folder only (`me` = _store/), so those rows were never read: the notes
// looked lost. FlattenPersonal moves every row of such a folder to
// _store/<table>/<id>.json in one commit, the same move personal_store_flatten
// makes for a 0.1.17 local personal store: every personal note then loads in
// every repo, as every 0.1.18 personal note does. A row found in two
// folders (the person's actor row is in each) is merged with the row
// merger; a folder's _meta.json (the 0.1.17 folder's project id) is
// dropped, its _purged.json merged into _store/'s. The old paths stay in
// the history.
//
// A folder is 0.1.17's when its _meta.json says `"_v": 1` (0.1.18 writes
// `_v: 2`, and nothing rewrites a folder it never attaches), when it is
// everywhere/, or when it still has a .lore/data folder and no _meta.json.
// A folder a person made since with `--to me/<folder>` is left alone.

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// PersonalFlattenSubject is the title of the commit FlattenPersonal makes.
const PersonalFlattenSubject = "lore: personal store — lore 0.1.17's per-org notes move from <folder>/ to _store/"

// personalEverywhereV017 is lore 0.1.17's no-org personal folder.
const personalEverywhereV017 = "everywhere"

// PersonalFlattenResult is what FlattenPersonal did.
type PersonalFlattenResult struct {
	// Commit is the commit made ("" when there was nothing to move).
	Commit string
	// Folders are the 0.1.17 folders whose rows moved, sorted.
	Folders []string
	// Rows counts the row files moved (a row merged into one already at
	// _store/ counts too).
	Rows int
	// Merged lists the _store/ files a moved row was merged into.
	Merged []string
	// Kept lists the _store/ files a moved row could not be merged into:
	// the _store/ copy is kept, the other is in the history.
	Kept []string
}

// PersonalFoldersV017At lists the top folders of the store clone at dir
// that lore 0.1.17 made for personal notes (see the file comment). Files
// only, no git: the first-run step calls it on every command.
func PersonalFoldersV017At(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, ferr := rowfs.CleanFolder(e.Name())
		if ferr != nil || f == rowfs.FolderRoot || strings.HasPrefix(f, "_") || strings.HasPrefix(f, ".") {
			continue
		}
		if personalFolderV017(filepath.Join(dir, f), f) {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// personalFolderV017 reports whether the folder at p (named f) is a 0.1.17
// personal folder that still holds files.
func personalFolderV017(p, f string) bool {
	oldData := filepath.Join(p, rowfs.LoreDir, rowfs.DataDir)
	metas := []string{filepath.Join(oldData, rowfs.MetaFileName), filepath.Join(p, rowfs.MetaFileName)}
	hasMeta := false
	for _, m := range metas {
		b, err := os.ReadFile(filepath.Clean(m))
		if err != nil {
			continue
		}
		hasMeta = true
		if metaIsV1(b) {
			return true
		}
	}
	if f == personalEverywhereV017 {
		return hasRowFiles(p)
	}
	return !hasMeta && isDir(oldData)
}

// metaIsV1 reports a _meta.json lore 0.1.17 wrote (`"_v": 1`).
func metaIsV1(b []byte) bool {
	for _, m := range [][]byte{[]byte(`"_v": 1,`), []byte(`"_v":1,`), []byte(`"_v": 1}`), []byte(`"_v":1}`), []byte("\"_v\": 1\n")} {
		if bytes.Contains(b, m) {
			return true
		}
	}
	return false
}

// hasRowFiles reports whether any row file sits under the folder at p, in
// either layout.
func hasRowFiles(p string) bool {
	found := false
	_ = filepath.WalkDir(p, func(q string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if d.IsDir() || filepath.Ext(q) != ".json" {
			return nil
		}
		rel := filepath.Base(filepath.Dir(q)) + "/" + filepath.Base(q)
		if _, _, ok := rowfs.ParseRowPath(rel); ok {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// personalMoveOf is where a file of 0.1.17 personal folder f goes: a row
// file (either layout) → _store/<table>/<id>.json, _purged.json →
// _store/_purged.json (merged). drop is true for the folder's _meta.json.
// ok is false for any other file (left where it is).
func personalMoveOf(f, p string) (to string, drop, ok bool) {
	rest, found := strings.CutPrefix(p, f+"/")
	if !found {
		return "", false, false
	}
	rest = strings.TrimPrefix(rest, rowfs.LoreDir+"/"+rowfs.DataDir+"/")
	switch rest {
	case rowfs.MetaFileName:
		return "", true, true
	case rowfs.PurgedFileName:
		return path.Join(rowfs.StoreDataDir, rowfs.PurgedFileName), false, true
	}
	if _, _, rok := rowfs.ParseRowPath(rest); rok && strings.Count(rest, "/") == 1 {
		return path.Join(rowfs.StoreDataDir, rest), false, true
	}
	return "", false, false
}

// FlattenPersonal moves the rows of the clone's 0.1.17 personal folders to
// _store/ in one commit (see the file comment). It does not push: the
// caller pushes per the store's push mode. Close the Backend and open the
// store again after it, as after FlattenLayout.
func (b *Backend) FlattenPersonal(ctx context.Context) (PersonalFlattenResult, error) {
	var res PersonalFlattenResult
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
	folders := PersonalFoldersV017At(b.work)
	if len(folders) == 0 {
		return res, nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}})
	if err != nil {
		return res, errs.Wrap(errs.Internal, "git: list the store's files", err)
	}
	index, err := b.indexEntries(ctx)
	if err != nil {
		return res, err
	}
	var moves []flatMove
	var drops []string
	seen := map[string]bool{}
	for _, p := range splitZ(out) {
		if seen[p] {
			continue
		}
		seen[p] = true
		f, _, _ := strings.Cut(p, "/")
		if !slices.Contains(folders, f) {
			continue
		}
		to, drop, ok := personalMoveOf(f, p)
		switch {
		case !ok:
		case drop:
			drops = append(drops, p)
		default:
			moves = append(moves, flatMove{from: p, to: to})
		}
	}
	slices.SortFunc(moves, func(a, c flatMove) int { return strings.Compare(a.from, c.from) })
	var info bytes.Buffer
	moved := map[string]bool{}
	for _, m := range moves {
		line, merged, kept, merr := b.flattenOne(ctx, m, index)
		if merr != nil {
			return res, merr
		}
		info.WriteString(line)
		// two folders may hold the same row: the second move must see the
		// first one's file as tracked at _store/, so it merges into it
		if _, tracked := index[m.to]; !tracked {
			if e, was := index[m.from]; was {
				index[m.to] = e
			}
		}
		if merged {
			res.Merged = append(res.Merged, m.to)
		}
		if kept {
			res.Kept = append(res.Kept, m.to)
		}
		if path.Base(m.to) != rowfs.PurgedFileName {
			res.Rows++
		}
		f, _, _ := strings.Cut(m.from, "/")
		moved[f] = true
	}
	for _, p := range drops {
		if _, tracked := index[p]; tracked {
			info.WriteString("0 " + zeroOID + "\t" + p + "\n")
		}
		_ = os.Remove(filepath.Join(b.work, filepath.FromSlash(p))) // in the history; a missing file is fine
		f, _, _ := strings.Cut(p, "/")
		moved[f] = true
	}
	if info.Len() > 0 {
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-index", "--index-info"}, stdin: info.Bytes()}); err != nil {
			return res, errs.Wrap(errs.Internal, "git: stage the moved personal rows", err)
		}
	}
	for f := range moved {
		res.Folders = append(res.Folders, f)
		removeEmptyTree(filepath.Join(b.work, f))
	}
	sort.Strings(res.Folders)
	if _, derr := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--cached", "--quiet"}}); derr == nil {
		return res, nil // nothing staged: only untracked files moved, or nothing at all
	}
	// a write: the clone's git identity and hand-git set-up that an open
	// leaves pending happen now, so the commit is signed as the person's
	// identity for this store (a clone moved from 0.1.17 has none yet)
	b.setUpHandGitOnce(ctx)
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return res, err
	}
	msg := PersonalFlattenSubject + "\n\n" + "lore 0.1.17 filed personal notes per org (" + strings.Join(res.Folders, "/, ") + "/); this lore reads a personal store's " + rowfs.StoreDataDir + "/ folder." +
		"\nEvery row moves there (a row in two folders is merged), so each note loads in every repo, as every 0.1.18 personal note does." +
		"\nThe old paths stay in this store's history."
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "-m", b.cfg.withTrailers(msg)}, env: env}); err != nil {
		return res, errs.Wrap(errs.Internal, "git: commit the personal store move", err).
			WithHint("check the clone with git -C " + b.work + " status, then run the command again (lore finishes the move)")
	}
	if res.Commit, err = b.head(ctx); err != nil {
		return res, err
	}
	b.dropCaches()
	return res, nil
}

// removeEmptyTree removes every empty folder under dir, deepest first, and
// dir itself when it ends up empty; a folder still holding a file stays.
func removeEmptyTree(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // only an empty folder goes
	}
}
