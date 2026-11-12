package rowfs

// storelayout.go — where a folder's row files sit, per kind of checkout
// (plain files round, brief 1.2, D-c, D-d):
//
//	LayoutStore (a store: a git store's clone, a folder store)
//	    <top>/_store/<table>/<id>.json      the root folder "." (the store's
//	                                        own rows: actors, machines, …)
//	    <top>/<folder>/<table>/<id>.json    any other folder ("org", "team/web")
//	    <folder>/_meta.json, _purged.json   beside the folder's table folders
//	                                        (_store/_meta.json for the root)
//	    lore's own state: .git/lore/ (git clone) or StoreStateDir (folder store)
//	LayoutRepo (a code repo; also how stores looked before this round)
//	    <top>/.lore/data/<table>/<id>.json  (.lore/ keeps lore apart from code)
//	    lore's own state: .lore/lock, .lore/state/
//
// A store folder and a table folder are told apart by name: a table folder
// is named after a table (IsTableDir), and no folder segment may be one
// (CleanFolder), so a walker never has to guess. The root folder's
// _store/ can never be a store folder either: no folder segment may start
// with "_" (D-ad). Stores of the first plain layout (lore_store: 3) kept
// the root's tables at the top; the git backend moves them into _store/
// (FlatRootLeft, git.Backend.FlattenLayout).

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/schema"
)

// Layout says where the row files of a Root's folders sit.
type Layout uint8

const (
	// LayoutStore is a store checkout: a folder's rows sit directly under
	// it (<folder>/<table>/<id>.json; the root folder's in _store/).
	LayoutStore Layout = iota
	// LayoutRepo is a code repo: rows under <folder>/.lore/data (the
	// repo's root folder only, in practice). Stores written before the
	// plain-files round used it too (IsOldStoreLayout).
	LayoutRepo
)

// String names the layout in messages.
func (l Layout) String() string {
	if l == LayoutRepo {
		return "repo (.lore/data)"
	}
	return "store"
}

// StoreStateDir is a folder store's own state folder at its top: the lock
// and the pending audit journal (D-c). A git store clone keeps the same
// in .git/lore instead; a code repo in .lore/.
const StoreStateDir = ".lore-state"

// StoreDataDir is where a store keeps its root folder's rows (D-ad): the
// store's own rows (actors, machines, store-wide rules), apart from every
// project folder and the org folder at the store's top. Its leading "_"
// is what no folder name may start with (CleanFolder), so it can never be
// mistaken for, or clash with, a project folder.
const StoreDataDir = "_store"

// DataPath is the data dir of a clean folder, relative to the root and
// slash separated: "_store" (a store's root folder), "org", "team/web";
// ".lore/data", "org/.lore/data" in LayoutRepo. Join paths under it with
// path.Join (which drops a leading "./"), never by string concatenation.
func (l Layout) DataPath(folder string) string {
	if l == LayoutRepo {
		if folder == FolderRoot || folder == "" {
			return LoreDir + "/" + DataDir
		}
		return folder + "/" + LoreDir + "/" + DataDir
	}
	if folder == "" || folder == FolderRoot {
		return StoreDataDir
	}
	return folder
}

// StorePath is the root-relative slash path of a file at rel (relative to
// the folder's data dir, "tasks/tsk_….json" or "_meta.json").
func (l Layout) StorePath(folder, rel string) string {
	return path.Join(l.DataPath(folder), rel)
}

// Pathspecs are the git pathspecs that hold exactly the row files of a
// folder (plus, in LayoutStore, nothing of the folders below it): one per
// table folder in LayoutStore, the data dir in LayoutRepo. Callers still
// check each path with SplitRowPath.
func (l Layout) Pathspecs(folder string) []string {
	if l == LayoutRepo {
		return []string{l.DataPath(folder)}
	}
	names := ReservedFolderNames()
	out := make([]string, 0, len(names))
	for _, t := range names {
		out = append(out, l.StorePath(folder, t))
	}
	return out
}

// SplitRowPath takes a root-relative slash path apart: the folder that
// holds the row file and the file's path in that folder's data dir
// ("tasks/tsk_….json"). ok is false for anything that is not a row file of
// some folder in this layout.
func (l Layout) SplitRowPath(p string) (folder, rel string, ok bool) {
	if l == LayoutRepo {
		return splitRepoRowPath(p)
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 {
		return "", "", false
	}
	rel = segs[len(segs)-2] + "/" + segs[len(segs)-1]
	if !IsTableDir(segs[len(segs)-2]) {
		return "", "", false
	}
	if _, _, ok := ParseRowPath(rel); !ok {
		return "", "", false
	}
	f, ok := storeFolderOf(strings.Join(segs[:len(segs)-2], "/"))
	if !ok {
		return "", "", false
	}
	return f, rel, true
}

// storeFolderOf is the clean folder whose LayoutStore data dir is dir:
// FolderRoot for StoreDataDir; ok false for the store's top itself (a
// table folder there is the first plain layout's, FlatRootLeft) and for
// anything that is no folder name.
func storeFolderOf(dir string) (string, bool) {
	switch dir {
	case StoreDataDir:
		return FolderRoot, true
	case "", FolderRoot:
		return "", false
	}
	f, err := CleanFolder(dir)
	if err != nil || f == FolderRoot {
		return "", false
	}
	return f, true
}

// SplitFolderFile is SplitRowPath for a folder's own files (_meta.json,
// _purged.json): the folder and the file name; ok false otherwise.
func (l Layout) SplitFolderFile(p string) (folder, name string, ok bool) {
	dir, name := path.Split(p)
	if name != MetaFileName && name != PurgedFileName {
		return "", "", false
	}
	dir = strings.TrimSuffix(dir, "/")
	if l == LayoutStore {
		f, ok := storeFolderOf(dir)
		return f, name, ok
	}
	d, found := strings.CutSuffix(dir, LoreDir+"/"+DataDir)
	if !found {
		return "", "", false
	}
	dir = strings.TrimSuffix(d, "/")
	f, err := CleanFolder(dir)
	if err != nil {
		return "", "", false
	}
	return f, name, true
}

// splitRepoRowPath is SplitRowPath of LayoutRepo: <folder>/.lore/data/<table>/<id>.json.
func splitRepoRowPath(p string) (folder, rel string, ok bool) {
	const data = LoreDir + "/" + DataDir + "/"
	folder = FolderRoot
	rest, found := strings.CutPrefix(p, data)
	if !found {
		i := strings.Index(p, "/"+data)
		if i < 0 {
			return "", "", false
		}
		segs := p[:i]
		for seg := range strings.SplitSeq(segs, "/") {
			if !folderSegment.MatchString(seg) {
				return "", "", false
			}
		}
		folder, rest = segs, p[i+1+len(data):]
	}
	if _, _, ok := ParseRowPath(rest); !ok {
		return "", "", false
	}
	return folder, rest, true
}

// reservedOnce is the sorted table names, built once.
var reservedOnce = sync.OnceValue(func() []string {
	defs := schema.Builtin()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name.String())
	}
	slices.Sort(out)
	return slices.Compact(out)
})

// ReservedFolderNames are the names no store folder segment may take: the
// table names (sorted). Error messages list them.
func ReservedFolderNames() []string { return slices.Clone(reservedOnce()) }

// IsTableDir reports whether a folder name is a table's folder (a table
// name lore knows).
func IsTableDir(name string) bool {
	_, found := slices.BinarySearch(reservedOnce(), name)
	return found
}

// OldLayoutPath reports whether a root-relative slash path of a store tree
// is in the layout stores had before the plain-files round
// (<folder>/.lore/data/…): the one test of "old layout" for a path list
// (git ls-tree output).
func OldLayoutPath(p string) bool {
	const data = LoreDir + "/" + DataDir
	return p == data || strings.HasPrefix(p, data+"/") || strings.Contains(p, "/"+data+"/") || strings.HasSuffix(p, "/"+data)
}

// IsOldStoreLayout reports whether the store checkout at top (a store's
// clone or folder) still keeps rows in the old layout: a .lore/data folder
// at its top or in any folder (the move to LayoutStore is brief 1.6). It
// only reads; .git and other dot folders are not looked into.
func IsOldStoreLayout(top string) (bool, error) {
	found := false
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == LoreDir {
			if st, serr := os.Stat(filepath.Join(p, DataDir)); serr == nil && st.IsDir() {
				found = true
				return filepath.SkipAll
			}
			return filepath.SkipDir
		}
		if p != top && (strings.HasPrefix(d.Name(), ".") || d.Name() == StoreDataDir || IsTableDir(d.Name())) {
			return filepath.SkipDir
		}
		return nil
	})
	return found, err
}

// FlatRootLeft reports whether the store checkout at top still keeps root
// folder files at its top, as the first plain layout did (lore_store: 3,
// before D-ad): a row file in a table folder (<table>/<id>.json) or the
// root's _meta.json or _purged.json right at the top. They belong in
// _store/ now; the git backend's layout commit moves them
// (git.Backend.FlattenLayout). A table folder holding no row file (a
// person's own notes in it) does not count: doctor names those. Files
// only.
func FlatRootLeft(top string) bool {
	entries, err := os.ReadDir(top)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case !e.IsDir() && (name == MetaFileName || name == PurgedFileName):
			return true
		case e.IsDir() && IsTableDir(name):
			files, rerr := os.ReadDir(filepath.Join(top, name))
			if rerr != nil {
				continue
			}
			for _, f := range files {
				if _, _, ok := ParseRowPath(name + "/" + f.Name()); ok && !f.IsDir() {
					return true
				}
			}
		}
	}
	return false
}

// StoreFolders lists the folders of the store checkout at top, the root
// excepted, sorted: every folder holding a _meta.json (a folder lore made;
// <folder>/.lore/data/_meta.json for one not moved yet). Table folders
// and dot folders (.git, .lore-state) are never walked into.
func StoreFolders(top string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || p == top {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || name == StoreDataDir || IsTableDir(name) {
			return filepath.SkipDir
		}
		rel, rerr := filepath.Rel(top, p)
		if rerr != nil {
			return rerr
		}
		f, cerr := CleanFolder(filepath.ToSlash(rel))
		if cerr != nil {
			return filepath.SkipDir // not a name lore makes: nothing below is a folder of lore's
		}
		if HasStoreFolder(top, f) {
			out = append(out, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

// HasStoreFolder reports whether the store checkout at top has the clean
// folder f: the root always; any other folder when its _meta.json exists
// (in the old layout: its .lore/data folder). A folder that only holds
// folders below it ("team" of "team/web") is not one.
func HasStoreFolder(top, f string) bool {
	if f == FolderRoot || f == "" {
		return true
	}
	if isRegular(filepath.Join(top, filepath.FromSlash(LayoutStore.StorePath(f, MetaFileName)))) {
		return true
	}
	return isDir(filepath.Join(top, filepath.FromSlash(LayoutRepo.DataPath(f))))
}

// RowFileCount counts the folder's row files (tombstones included) as a
// scan sees them; display only.
func (f *Folder) RowFileCount() (int, error) {
	files, _, err := f.scan()
	return len(files), err
}
