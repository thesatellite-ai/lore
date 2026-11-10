package wire

// trash_local_purged.go — the repo's purged ids for `lore trash purge
// --local --purged` and its doctor line (DELETION 3.10): read from the
// checkout's own `.lore/data/_purged.json` through rowfs, so the format
// check (an unreadable or newer file is an error, never an empty set) is
// the one every purge uses.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

var _ opstrash.RepoPurged = (*App)(nil)

// RepoPurged implements opstrash.RepoPurged.
func (a *App) RepoPurged(ctx context.Context) (map[string]bool, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	root, err := rowfs.Open(top, a.opts.rowfsOptions()...)
	if err != nil {
		return nil, err
	}
	f, err := root.Folder(rowfs.FolderRoot)
	if err != nil {
		return nil, err
	}
	return f.Purged()
}
