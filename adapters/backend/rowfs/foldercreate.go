package rowfs

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Create makes the folder exist on purpose (`lore store folder add`,
// STORE TARGETS): it writes the folder's _meta.json exactly as the
// folder's first row write would (ensureMeta: the shared copy when a
// MetaSource has one, else the natural prj_ id), and returns the written
// paths relative to the store root (none when the folder already had its
// _meta.json, so a second run changes nothing). The caller holds the
// store lock and commits the paths. The root folder always exists.
func (f *Folder) Create(ctx context.Context) ([]string, error) {
	if f.name == FolderRoot {
		return nil, errs.New(errs.InvalidInput, "rowfs: the store root is not a folder to create")
	}
	var a Applied
	if err := f.ensureMeta(ctx, &a); err != nil {
		return nil, err
	}
	return a.Paths, nil
}
