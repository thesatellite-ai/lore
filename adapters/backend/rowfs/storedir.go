package rowfs

// storedir.go — a folder store (file://, no git) of the first plain
// layout (lore_store: 3) moves its root folder's files into _store/ when
// this lore first opens it for writing (D-ad). A git store gets the same
// move as one commit (git.Backend.FlattenLayout); a folder store has no
// history, so the files are renamed in place, under the store lock:
//
//	<table>/<id>.json       →  _store/<table>/<id>.json
//	_meta.json, _purged.json →  _store/_meta.json, _store/_purged.json
//	lore-store.yaml          lore_store: 4 (an older lore refuses it)
//
// A file already at its _store/ place (a teammate's lore moved it, or a
// run cut short) keeps the _store/ copy; the one at the top is left for a
// person (doctor's store files check names it). Every rule reads what is
// on disk, so a run cut short is finished by the next one.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
)

// StoreDirMove is what MoveRootToStoreDir did.
type StoreDirMove struct {
	// Moved lists the files moved, root-relative slash paths of where they
	// were ("memories/mem_….json", "_meta.json"), in the order moved.
	Moved []string
	// Left lists the files left at the top because _store/ already held
	// one of the same name.
	Left []string
	// Marker is true when lore-store.yaml was rewritten to MarkerVersion.
	Marker bool
}

// Change is the move as a LayoutChange (for the line lore prints).
func (m StoreDirMove) Change() LayoutChange {
	c := LayoutChange{Moved: len(m.Moved)}
	if m.Marker {
		c.Marker = MarkerVersion
	}
	return c
}

// NeedsStoreDirMove reports whether the folder store at dir needs
// MoveRootToStoreDir: its marker names the first plain layout (3), or its
// root folder's files sit at the top (FlatRootLeft). Files only.
func NeedsStoreDirMove(dir string) bool {
	m, err := ReadMarker(dir)
	if err != nil || m.Version < MarkerFirstStores || m.ID.IsZero() {
		return false // no marker, or a v0.1.17 store: `lore migrate run` first
	}
	if old, oerr := IsOldStoreLayout(dir); oerr == nil && old {
		return false // the layout before the plain files round is read in place (Folder.OldLayout)
	}
	return m.Version < MarkerVersion || FlatRootLeft(dir)
}

// MoveRootToStoreDir moves the folder store's root folder files into
// _store/ and sets lore_store to MarkerVersion (see the file comment). It
// takes the store lock.
func (r *Root) MoveRootToStoreDir(ctx context.Context) (StoreDirMove, error) {
	var res StoreDirMove
	lock, err := r.Lock(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = lock.Release() }() // every rename is done or not by then
	if !NeedsStoreDirMove(r.dir) {
		return res, nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return res, errs.FileIO("read", r.dir, err)
	}
	move := func(rel string) error {
		from := filepath.Join(r.dir, filepath.FromSlash(rel))
		to := filepath.Join(r.dir, StoreDataDir, filepath.FromSlash(rel))
		if _, serr := os.Lstat(to); serr == nil {
			res.Left = append(res.Left, rel)
			return nil
		} else if !errors.Is(serr, fs.ErrNotExist) {
			return errs.FileIO("read", to, serr)
		}
		if merr := os.MkdirAll(filepath.Dir(to), dirMode); merr != nil {
			return errs.FileIO("create the row folder", filepath.Dir(to), merr)
		}
		if rerr := os.Rename(from, to); rerr != nil {
			return errs.FileIO("move", from, rerr)
		}
		res.Moved = append(res.Moved, rel)
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case !e.IsDir() && (name == MetaFileName || name == PurgedFileName):
			if err := move(name); err != nil {
				return res, err
			}
		case e.IsDir() && IsTableDir(name):
			files, rerr := os.ReadDir(filepath.Join(r.dir, name))
			if rerr != nil {
				return res, errs.FileIO("read", filepath.Join(r.dir, name), rerr)
			}
			for _, f := range files {
				rel := name + "/" + f.Name()
				if _, _, ok := ParseRowPath(rel); !ok || f.IsDir() {
					continue // not a row file: a person's, left where it is
				}
				if err := move(rel); err != nil {
					return res, err
				}
			}
			_ = os.Remove(filepath.Join(r.dir, name)) // only an emptied folder goes
		}
	}
	m, err := ReadMarker(r.dir)
	if err != nil {
		return res, err
	}
	if m.Version < MarkerVersion {
		if err := WriteMarker(r.dir, m); err != nil { // Encode writes MarkerVersion
			return res, err
		}
		res.Marker = true
	}
	return res, nil
}
