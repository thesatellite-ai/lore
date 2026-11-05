package engine

// LocateHidden: is a row this checkout cannot read nevertheless in one of
// the stores it attaches, in a data folder it does not attach? A client
// repo attaches only its linked project folder of an org store; a pointer
// it stores at a row of the store's root or org folder must be refused by
// F15 (E_LEAK_BLOCKED, the direction rule) rather than answered "no such
// row" (final smoke; PRINCIPLE EXACT ERRORS). Callers ask only after Get
// answered E_NOT_FOUND, and refuse on any error (fail closed).
//
//	for each attached store, in stores.yaml order:
//	    Backend is a FolderFinder? ─► FindInFolders(table, id) ─► hit
//	no backend can tell ─► not found (Get's answer stands)

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// FolderFinder is an optional backend upgrade: the data folder of the
// backend's store (any folder, not only the one it opened) holding a file
// for table/id, live or tombstone. The git and folder backends implement
// it; a backend without it is not asked.
type FolderFinder interface {
	FindInFolders(ctx context.Context, table row.Table, id ids.AnyID) (folder string, found bool, err error)
}

// HiddenHit is where LocateHidden found a row: the attached store whose
// branch holds it, and the data folder (rowfs folder name) it is in.
type HiddenHit struct {
	Store  store.Source
	Folder string
}

// LocateHidden looks for ref in every data folder of each attached
// store's branch. found is false when none holds it. Any read error is
// returned: the caller refuses (fail closed).
func (e *Engine) LocateHidden(ctx context.Context, ref Ref) (HiddenHit, bool, error) {
	table := ref.Table
	if table == "" {
		def, ok := e.reg.ByPrefix(ref.ID.Prefix())
		if !ok {
			return HiddenHit{}, false, errs.New(errs.NotFound, "engine: no table holds ids like "+ref.ID.String())
		}
		table = def.Name
	}
	for _, name := range e.order {
		a, err := e.attached(name)
		if err != nil {
			return HiddenHit{}, false, err
		}
		ff, ok := folderFinder(a.Backend)
		if !ok {
			continue
		}
		folder, found, err := ff.FindInFolders(ctx, table, ref.ID)
		if err != nil {
			return HiddenHit{}, false, err
		}
		if found {
			return HiddenHit{Store: name, Folder: folder}, true, nil
		}
	}
	return HiddenHit{}, false, nil
}

// folderFinder is b, or the first backend under its wrappers, that is a
// FolderFinder: wrappers do not forward the optional upgrade.
func folderFinder(b store.Backend) (FolderFinder, bool) {
	for b != nil {
		if ff, ok := b.(FolderFinder); ok {
			return ff, true
		}
		u, ok := b.(unwrapper)
		if !ok {
			return nil, false
		}
		b = u.Unwrap()
	}
	return nil, false
}
