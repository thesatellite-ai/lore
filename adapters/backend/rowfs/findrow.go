package rowfs

// FindRow: where one row lives among ALL data folders of a store (the
// root, project folders, org). The engine asks it only after a lookup in
// the attached folder missed, so a pointer from a client repo at a row of
// a folder that repo does not attach is refused by F15 (E_LEAK_BLOCKED)
// instead of answered "not found" (final smoke, PRINCIPLE EXACT ERRORS).
//
//	WalkDir(root) ─► skip .git and dot dirs except .lore
//	              ─► <folder>/.lore/data/<table>/<id>.json exists? ─► folder

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
	tail := filepath.FromSlash(LoreDir + "/" + DataDir + "/" + RowPath(table, id))
	werr := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if p != r.dir && strings.HasPrefix(name, ".") {
			return filepath.SkipDir // .git, .lore (looked at directly below), tool dirs
		}
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
