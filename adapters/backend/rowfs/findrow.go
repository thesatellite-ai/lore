package rowfs

// FindRow: where one row lives among ALL data folders of a store (the
// root, project folders, org). The engine asks it only after a lookup in
// the attached folder missed, so a pointer from a client repo at a row of
// a folder that repo does not attach is refused by F15 (E_LEAK_BLOCKED)
// instead of answered "not found" (final smoke, PRINCIPLE EXACT ERRORS).
//
//	WalkDir(root) ─► skip .git, dot dirs, table folders and _store/
//	              ─► _store/<table>/<id>.json at the top? ─► the root folder
//	              ─► <folder>/<table>/<id>.json exists? ─► folder
//	                 (a code repo: <folder>/.lore/data/<table>/<id>.json)

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// errFound stops the walk at the first hit.
var errFound = errors.New("rowfs: found")

// FindRow reports the first data folder of the store (FolderRoot, a
// project folder or org) that holds a file for table/id, a live row or a
// tombstone. found is false when no folder holds one. A folder that cannot
// be read is an error, never "not found" (callers fail closed).
func (r *Root) FindRow(table row.Table, id ids.AnyID) (folder string, found bool, err error) {
	if !table.Valid() || id.IsZero() {
		return "", false, errs.New(errs.InvalidInput, "rowfs: find needs a table and an id")
	}
	rel := filepath.FromSlash(RowPath(table, id))
	// tailsAt are the places of the file below the folder at p: the
	// store's root folder keeps its rows in _store/ (StoreDataDir), any
	// other store folder right under it; a folder not moved yet (and a code
	// repo) under .lore/data (Folder.OldLayout)
	old := filepath.FromSlash(LayoutRepo.StorePath(FolderRoot, RowPath(table, id)))
	tailsAt := func(p string) []string {
		switch {
		case r.layout == LayoutRepo:
			return []string{old}
		case p == r.dir:
			return []string{filepath.FromSlash(LayoutStore.StorePath(FolderRoot, RowPath(table, id))), old}
		}
		return []string{rel, old}
	}
	werr := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if p != r.dir && (strings.HasPrefix(name, ".") || (r.layout == LayoutStore && (IsTableDir(name) || strings.HasPrefix(name, "_")))) {
			return filepath.SkipDir // .git, .lore (looked at directly below), tool dirs; a store's table folders and _store/ (looked at from the top)
		}
		for _, tail := range tailsAt(p) {
			if _, serr := os.Lstat(filepath.Join(p, tail)); serr == nil {
				rel, rerr := filepath.Rel(r.dir, p)
				if rerr != nil {
					return rerr
				}
				folder = filepath.ToSlash(rel)
				if folder == "." {
					folder = FolderRoot
				}
				return errFound
			} else if !errors.Is(serr, fs.ErrNotExist) {
				return serr
			}
		}
		return nil
	})
	switch {
	case errors.Is(werr, errFound):
		return folder, true, nil
	case werr != nil:
		return "", false, errs.Wrap(errs.Internal, "rowfs: look for "+id.String()+" in the store's folders", werr)
	}
	return "", false, nil
}
