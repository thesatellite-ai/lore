package rowfs

// readtable.go — one table of a data folder, and the same table of every
// other folder of a store, read straight from the row files (devco
// D-793). The reference resolver needs the resources and kinds of the
// sibling folders of a personal store (a journal note naming a person
// kept in the work folder); opening those folders would scan every row
// file in them (a feeds folder holds thousands), so only the one table
// folder is listed.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// FolderRows is the rows of one table in one data folder of a store.
type FolderRows struct {
	Folder string // the folder's name (FolderRoot for the store root)
	Rows   []store.Row
}

// ReadTable reads every row file of table in this folder, tombstones
// included (Deleted rows), in file name order. A table folder that does
// not exist is no rows; a file that is not a row file is skipped, as a
// folder scan skips it (Problems reports it).
func (f *Folder) ReadTable(ctx context.Context, table row.Table) ([]store.Row, error) {
	if !table.Valid() {
		return nil, errs.New(errs.InvalidInput, "rowfs: read table needs a table, got "+quote(table.String()))
	}
	if err := checkChain(f.root.dir, f.rel); err != nil {
		return nil, err
	}
	des, err := os.ReadDir(filepath.Join(f.dataDir, table.String()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: read table folder "+quote(f.storePath(table.String())), err)
	}
	var out []store.Row
	chains := chainMemo{}
	for _, de := range des {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !de.Type().IsRegular() || !strings.HasSuffix(de.Name(), RowFileExt) {
			continue
		}
		rel := table.String() + "/" + de.Name()
		if _, _, ok := ParseRowPath(rel); !ok {
			continue
		}
		r, ok, err := f.currentIn(rel, chains)
		if err != nil {
			return nil, err // currentIn explains a bad file
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// ReadSiblingTable reads table in every data folder of this store except
// the folder named except (the one the caller already reads): the root
// folder first, then the others in StoreFolders order. Folders with no
// such rows are left out. A code repo (LayoutRepo) has no sibling folders.
func (r *Root) ReadSiblingTable(ctx context.Context, except string, table row.Table) ([]FolderRows, error) {
	if r.layout != LayoutStore {
		return nil, nil
	}
	skip, err := CleanFolder(except)
	if err != nil {
		return nil, err
	}
	names, err := StoreFolders(r.dir)
	if err != nil {
		return nil, err
	}
	var out []FolderRows
	for _, name := range append([]string{FolderRoot}, names...) {
		if name == skip {
			continue
		}
		f, err := r.Folder(name)
		if err != nil {
			return nil, err
		}
		rows, err := f.ReadTable(ctx, table)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			out = append(out, FolderRows{Folder: name, Rows: rows})
		}
	}
	return out, nil
}

// FolderRow is one row file and the data folder holding it.
type FolderRow struct {
	Folder string // the folder's name (FolderRoot for the store root)
	Row    store.Row
}

// GetSiblingRow reads table/id in every data folder of this store except
// the folder named except, in ReadSiblingTable's order, and returns each
// copy found (tombstones included). Why (bug hunt H-283, devco:D-1021):
// a ${row:<id>} reference written in one folder of a personal store to a
// row kept in another folder resolved only when both folders were
// attached, so a search's answer depended on how its cache was built; one
// file per folder is looked at, nothing is scanned. A code repo
// (LayoutRepo) has no sibling folders.
func (r *Root) GetSiblingRow(ctx context.Context, except string, table row.Table, id ids.AnyID) ([]FolderRow, error) {
	if r.layout != LayoutStore {
		return nil, nil
	}
	skip, err := CleanFolder(except)
	if err != nil {
		return nil, err
	}
	names, err := StoreFolders(r.dir)
	if err != nil {
		return nil, err
	}
	var out []FolderRow
	for _, name := range append([]string{FolderRoot}, names...) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if name == skip {
			continue
		}
		f, err := r.Folder(name)
		if err != nil {
			return nil, err
		}
		got, err := f.Get(table, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, FolderRow{Folder: name, Row: got})
	}
	return out, nil
}
